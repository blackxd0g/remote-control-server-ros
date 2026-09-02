package identity

import "context"

type Profile struct {
	Subject, Username, Email, DisplayName string
	EmailVerified                         bool
	Groups                                []string
}
type Authorization struct{ State, PollCode, URL string }

type Provider interface {
	Name() string
	Begin(context.Context, LoginContext) (Authorization, error)
	Callback(context.Context, string, string) error
}

type PasswordProvider interface {
	Name() string
	Authenticate(context.Context, string, string) (Profile, error)
}

type LoginContext struct{ RustDeskID, ClientUUID, Platform, ClientType, DeviceName, IP, UserAgent, LinkUserID string }
