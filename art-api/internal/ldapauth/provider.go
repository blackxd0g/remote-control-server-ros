package ldapauth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/identity"
	"github.com/go-ldap/ldap/v3"
)

var ErrInvalidCredentials = errors.New("invalid LDAP credentials")

type Config struct {
	URL, BindDN, BindPassword, BaseDN, UserFilter           string
	UsernameAttribute, EmailAttribute, DisplayNameAttribute string
	GroupAttribute                                          string
	StartTLS, InsecureSkipVerify                            bool
	Timeout                                                 time.Duration
}

type Provider struct{ config Config }

func New(config Config) (*Provider, error) {
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if config.UserFilter == "" {
		config.UserFilter = "(&(objectClass=person)(|(uid={username})(sAMAccountName={username})(userPrincipalName={username})))"
	}
	if config.UsernameAttribute == "" {
		config.UsernameAttribute = "uid"
	}
	if config.EmailAttribute == "" {
		config.EmailAttribute = "mail"
	}
	if config.DisplayNameAttribute == "" {
		config.DisplayNameAttribute = "displayName"
	}
	if config.GroupAttribute == "" {
		config.GroupAttribute = "memberOf"
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || (parsed.Scheme != "ldap" && parsed.Scheme != "ldaps") || parsed.Host == "" {
		return nil, errors.New("LDAP URL must use ldap:// or ldaps://")
	}
	if parsed.Scheme == "ldap" && !config.StartTLS {
		return nil, errors.New("plain LDAP requires StartTLS")
	}
	if config.BaseDN == "" || config.BindDN == "" || !strings.Contains(config.UserFilter, "{username}") {
		return nil, errors.New("LDAP bind DN, base DN and username filter are required")
	}
	return &Provider{config: config}, nil
}

func (p *Provider) Name() string { return "ldap" }

func (p *Provider) Authenticate(ctx context.Context, username, password string) (identity.Profile, error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return identity.Profile{}, ErrInvalidCredentials
	}
	connection, err := p.dial(ctx)
	if err != nil {
		return identity.Profile{}, fmt.Errorf("connect LDAP: %w", err)
	}
	defer connection.Close()
	if err = connection.Bind(p.config.BindDN, p.config.BindPassword); err != nil {
		return identity.Profile{}, fmt.Errorf("LDAP service bind: %w", err)
	}
	filter := strings.ReplaceAll(p.config.UserFilter, "{username}", ldap.EscapeFilter(strings.TrimSpace(username)))
	request := ldap.NewSearchRequest(p.config.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 5, false, filter,
		[]string{p.config.UsernameAttribute, p.config.EmailAttribute, p.config.DisplayNameAttribute, p.config.GroupAttribute}, nil)
	result, err := connection.Search(request)
	if err != nil {
		return identity.Profile{}, fmt.Errorf("LDAP user search: %w", err)
	}
	if len(result.Entries) != 1 {
		return identity.Profile{}, ErrInvalidCredentials
	}
	entry := result.Entries[0]
	if err = connection.Bind(entry.DN, password); err != nil {
		return identity.Profile{}, ErrInvalidCredentials
	}
	resolvedUsername := entry.GetAttributeValue(p.config.UsernameAttribute)
	if resolvedUsername == "" {
		resolvedUsername = strings.TrimSpace(username)
	}
	return identity.Profile{Subject: entry.DN, Username: resolvedUsername, Email: entry.GetAttributeValue(p.config.EmailAttribute), DisplayName: entry.GetAttributeValue(p.config.DisplayNameAttribute), Groups: entry.GetAttributeValues(p.config.GroupAttribute)}, nil
}

func (p *Provider) dial(ctx context.Context) (*ldap.Conn, error) {
	parsed, _ := url.Parse(p.config.URL)
	tlsConfig := &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12, InsecureSkipVerify: p.config.InsecureSkipVerify} //nolint:gosec -- explicit opt-in for private PKI
	dialer := &net.Dialer{Timeout: p.config.Timeout}
	connection, err := ldap.DialURL(p.config.URL, ldap.DialWithDialer(dialer), ldap.DialWithTLSConfig(tlsConfig))
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		connection.SetTimeout(time.Until(deadline))
	} else {
		connection.SetTimeout(p.config.Timeout)
	}
	if parsed.Scheme == "ldap" && p.config.StartTLS {
		if err = connection.StartTLS(tlsConfig); err != nil {
			connection.Close()
			return nil, err
		}
	}
	return connection, nil
}
