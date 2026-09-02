package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	addressbookservice "github.com/art-rustdesk/platform/art-api/internal/addressbook"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/google/uuid"
)

func (s *Server) listAddressBookGrants(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	values, err := s.addressBooks.Grants(request.Context(), principal.User, request.PathValue("bookID"))
	if err != nil {
		writeAddressBookError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, values)
}

func (s *Server) putAddressBookGrant(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var grant domain.AddressBookGrant
	if err := decodeJSON(request, &grant, 16<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid address book grant")
		return
	}
	grant.AddressBookID = request.PathValue("bookID")
	value, err := s.addressBooks.PutGrant(request.Context(), principal.User, grant)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(response, http.StatusBadRequest, "grant subject not found")
			return
		}
		writeAddressBookError(response, err)
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "address_book_grant_update", ActorUserID: principal.User.ID, Result: "success",
		Metadata: map[string]any{"address_book_id": value.AddressBookID, "subject_type": value.SubjectType, "subject_id": value.SubjectID, "permission": value.Permission}})
	writeJSON(response, http.StatusOK, value)
}

func (s *Server) deleteAddressBookGrant(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if err := s.addressBooks.DeleteGrant(request.Context(), principal.User, request.PathValue("bookID"), request.PathValue("grantID")); err != nil {
		writeAddressBookError(response, err)
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "address_book_grant_delete", ActorUserID: principal.User.ID, Result: "success",
		Metadata: map[string]any{"address_book_id": request.PathValue("bookID"), "grant_id": request.PathValue("grantID")}})
	response.WriteHeader(http.StatusNoContent)
}

type rustDeskAddressBookPeer struct {
	ID               string   `json:"id"`
	Username         string   `json:"username"`
	Hostname         string   `json:"hostname"`
	Alias            string   `json:"alias"`
	Platform         string   `json:"platform"`
	Tags             []string `json:"tags"`
	Hash             string   `json:"hash"`
	Password         string   `json:"password,omitempty"`
	ForceAlwaysRelay any      `json:"forceAlwaysRelay"`
	RDPPort          string   `json:"rdpPort"`
	RDPUsername      string   `json:"rdpUsername"`
	LoginName        string   `json:"loginName"`
	SameServer       bool     `json:"sameServer"`
}

// UnmarshalJSON deliberately accepts additional RustDesk peer fields. The
// official client sends transient model properties such as online, row_id,
// user_id and timestamps that are not part of our persistent address-book
// domain model. Keeping strict decoding for normal API DTOs is useful, but
// rejecting those client-owned compatibility fields breaks peer creation.
func (peer *rustDeskAddressBookPeer) UnmarshalJSON(data []byte) error {
	type compatiblePeer rustDeskAddressBookPeer
	var value compatiblePeer
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*peer = rustDeskAddressBookPeer(value)
	return nil
}

func (s *Server) legacyAddressBook(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	book, found, err := s.personalAddressBook(request, principal.User, false)
	if err != nil {
		writeAddressBookError(response, err)
		return
	}
	peers := make([]rustDeskAddressBookPeer, 0)
	if found {
		entries, listErr := s.repository.ListAddressBookEntries(request.Context(), book.ID)
		if listErr != nil {
			writeError(response, http.StatusInternalServerError, "address book unavailable")
			return
		}
		peers = clientPeers(entries)
	}
	data, err := json.Marshal(map[string]any{"peers": peers, "tags": []string{}, "tag_colors": "{}"})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "address book unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"data": string(data)})
}

func (s *Server) updateLegacyAddressBook(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var envelope struct {
		Data string `json:"data"`
	}
	if err := decodeJSON(request, &envelope, 2<<20); err != nil || envelope.Data == "" {
		writeError(response, http.StatusBadRequest, "invalid address book")
		return
	}
	var payload struct {
		Peers []rustDeskAddressBookPeer `json:"peers"`
	}
	if err := json.Unmarshal([]byte(envelope.Data), &payload); err != nil {
		writeError(response, http.StatusBadRequest, "invalid address book data")
		return
	}
	book, _, err := s.personalAddressBook(request, principal.User, true)
	if err != nil {
		writeAddressBookError(response, err)
		return
	}
	entries, ok := entriesFromClientPeers(book.ID, payload.Peers)
	if !ok {
		writeError(response, http.StatusBadRequest, "invalid address book peer")
		return
	}
	if err := s.repository.ReplaceAddressBookEntries(request.Context(), book.ID, entries); err != nil {
		writeError(response, http.StatusConflict, "address book contains duplicate peers")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "address_book_sync", ActorUserID: principal.User.ID, Result: "success",
		Metadata: map[string]any{"address_book_id": book.ID, "peer_count": len(entries)}})
	response.WriteHeader(http.StatusOK)
}

func (s *Server) clientPersonalAddressBook(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	book, _, err := s.personalAddressBook(request, principal.User, true)
	if err != nil {
		writeAddressBookError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"guid": book.ID, "name": book.Name, "owner": principal.User.Username, "rule": 3})
}

func (s *Server) clientAddressBookSettings(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{"max_peer_one_ab": 0})
}

func (s *Server) clientSharedAddressBooks(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	books, err := s.addressBooks.List(request.Context(), principal.User)
	if err != nil {
		writeAddressBookError(response, err)
		return
	}
	users, err := s.repository.ListUsers(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "address book owners unavailable")
		return
	}
	owners := make(map[string]string, len(users))
	for _, user := range users {
		owners[user.ID] = user.Username
	}
	profiles := make([]map[string]any, 0)
	for _, book := range books {
		if book.Kind != "shared" {
			continue
		}
		profiles = append(profiles, map[string]any{"guid": book.ID, "name": book.Name, "owner": owners[book.OwnerUserID], "note": "", "rule": clientPermissionRule(book.Permission)})
	}
	writeJSON(response, http.StatusOK, map[string]any{"total": len(profiles), "data": profiles})
}

func (s *Server) clientAddressBookPeers(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	bookID := strings.TrimSpace(request.URL.Query().Get("ab"))
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, bookID, addressbookservice.PermissionRead); err != nil {
		writeAddressBookError(response, err)
		return
	}
	entries, err := s.repository.ListAddressBookEntries(request.Context(), bookID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "address book unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"total": len(entries), "data": clientPeers(entries), "licensed_devices": 99999})
}

func (s *Server) clientAddressBookTags(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, request.PathValue("bookID"), addressbookservice.PermissionRead); err != nil {
		writeAddressBookError(response, err)
		return
	}
	tags, err := s.repository.ListAddressBookTags(request.Context(), request.PathValue("bookID"))
	if err != nil {
		writeError(response, http.StatusInternalServerError, "address book tags unavailable")
		return
	}
	writeJSON(response, http.StatusOK, tags)
}

func (s *Server) clientAddAddressBookTag(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	bookID := request.PathValue("bookID")
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, bookID, addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	var input struct {
		Name  string `json:"name"`
		Color int64  `json:"color"`
	}
	if err := decodeJSON(request, &input, 8<<10); err != nil || !validAddressBookTag(input.Name) {
		writeError(response, http.StatusBadRequest, "invalid address book tag")
		return
	}
	now := time.Now().UTC()
	value := domain.AddressBookTag{ID: uuid.NewString(), AddressBookID: bookID, Name: strings.TrimSpace(input.Name), Color: input.Color, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateAddressBookTag(request.Context(), value); err != nil {
		writeError(response, http.StatusConflict, "address book tag already exists")
		return
	}
	response.WriteHeader(http.StatusOK)
}

func (s *Server) clientRenameAddressBookTag(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	bookID := request.PathValue("bookID")
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, bookID, addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	var input struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := decodeJSON(request, &input, 8<<10); err != nil || !validAddressBookTag(input.Old) || !validAddressBookTag(input.New) {
		writeError(response, http.StatusBadRequest, "invalid address book tag")
		return
	}
	tags, err := s.repository.ListAddressBookTags(request.Context(), bookID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "address book tags unavailable")
		return
	}
	var value domain.AddressBookTag
	for _, tag := range tags {
		if tag.Name == strings.TrimSpace(input.Old) {
			value = tag
			break
		}
	}
	if value.ID == "" {
		writeError(response, http.StatusNotFound, "address book tag not found")
		return
	}
	oldName := value.Name
	value.Name, value.UpdatedAt = strings.TrimSpace(input.New), time.Now().UTC()
	if err := s.repository.UpdateAddressBookTag(request.Context(), value, oldName); err != nil {
		writeError(response, http.StatusConflict, "address book tag already exists")
		return
	}
	entries, _ := s.repository.ListAddressBookEntries(request.Context(), bookID)
	for _, entry := range entries {
		changed := false
		for index, tag := range entry.Tags {
			if tag == oldName {
				entry.Tags[index], changed = value.Name, true
			}
		}
		if changed {
			_ = s.repository.UpdateAddressBookEntry(request.Context(), entry)
		}
	}
	response.WriteHeader(http.StatusOK)
}

func (s *Server) clientUpdateAddressBookTag(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	bookID := request.PathValue("bookID")
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, bookID, addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	var input struct {
		Name  string `json:"name"`
		Color int64  `json:"color"`
	}
	if err := decodeJSON(request, &input, 8<<10); err != nil || !validAddressBookTag(input.Name) {
		writeError(response, http.StatusBadRequest, "invalid address book tag")
		return
	}
	tags, err := s.repository.ListAddressBookTags(request.Context(), bookID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "address book tags unavailable")
		return
	}
	for _, value := range tags {
		if value.Name == strings.TrimSpace(input.Name) {
			value.Color, value.UpdatedAt = input.Color, time.Now().UTC()
			if err := s.repository.UpdateAddressBookTag(request.Context(), value, value.Name); err != nil {
				writeAddressBookError(response, err)
				return
			}
			response.WriteHeader(http.StatusOK)
			return
		}
	}
	writeError(response, http.StatusNotFound, "address book tag not found")
}

func (s *Server) clientDeleteAddressBookTags(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	bookID := request.PathValue("bookID")
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, bookID, addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	var names []string
	if err := decodeJSON(request, &names, 16<<10); err != nil || len(names) == 0 {
		writeError(response, http.StatusBadRequest, "invalid address book tags")
		return
	}
	remove := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !validAddressBookTag(name) {
			writeError(response, http.StatusBadRequest, "invalid address book tag")
			return
		}
		remove[name] = struct{}{}
		if err := s.repository.DeleteAddressBookTag(request.Context(), bookID, name); err != nil && !errors.Is(err, domain.ErrNotFound) {
			writeAddressBookError(response, err)
			return
		}
	}
	entries, _ := s.repository.ListAddressBookEntries(request.Context(), bookID)
	for _, entry := range entries {
		filtered := entry.Tags[:0]
		for _, tag := range entry.Tags {
			if _, deleted := remove[tag]; !deleted {
				filtered = append(filtered, tag)
			}
		}
		if len(filtered) != len(entry.Tags) {
			entry.Tags = filtered
			_ = s.repository.UpdateAddressBookEntry(request.Context(), entry)
		}
	}
	response.WriteHeader(http.StatusOK)
}

func validAddressBookTag(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 64
}

func (s *Server) clientAddAddressBookPeer(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	bookID := request.PathValue("bookID")
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, bookID, addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	var peer rustDeskAddressBookPeer
	if err := decodeJSON(request, &peer, 64<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid address book peer")
		return
	}
	entries, ok := entriesFromClientPeers(bookID, []rustDeskAddressBookPeer{peer})
	if !ok || len(entries) != 1 {
		writeError(response, http.StatusBadRequest, "invalid address book peer")
		return
	}
	if err := s.repository.CreateAddressBookEntry(request.Context(), entries[0]); err != nil {
		writeError(response, http.StatusConflict, "peer already exists")
		return
	}
	response.WriteHeader(http.StatusOK)
}

func (s *Server) clientUpdateAddressBookPeer(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	bookID := request.PathValue("bookID")
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, bookID, addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	var peer rustDeskAddressBookPeer
	if err := decodeJSON(request, &peer, 64<<10); err != nil || strings.TrimSpace(peer.ID) == "" {
		writeError(response, http.StatusBadRequest, "invalid address book peer")
		return
	}
	entries, err := s.repository.ListAddressBookEntries(request.Context(), bookID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "address book unavailable")
		return
	}
	for _, entry := range entries {
		if entry.RustDeskID != peer.ID {
			continue
		}
		entry.Alias = strings.TrimSpace(peer.Alias)
		entry.Username = strings.TrimSpace(peer.Username)
		entry.Hostname = strings.TrimSpace(peer.Hostname)
		entry.Platform = strings.TrimSpace(peer.Platform)
		entry.Tags = normalizeAddressBookTags(peer.Tags)
		entry.ForceRelay = clientBool(peer.ForceAlwaysRelay)
		entry.RDPPort = strings.TrimSpace(peer.RDPPort)
		entry.RDPUsername = strings.TrimSpace(peer.RDPUsername)
		entry.LoginName = strings.TrimSpace(peer.LoginName)
		entry.SameServer = peer.SameServer
		if err := s.repository.UpdateAddressBookEntry(request.Context(), entry); err != nil {
			writeError(response, statusForStore(err), "address book peer not found")
			return
		}
		response.WriteHeader(http.StatusOK)
		return
	}
	writeError(response, http.StatusNotFound, "address book peer not found")
}

func (s *Server) clientDeleteAddressBookPeer(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	bookID := request.PathValue("bookID")
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, bookID, addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	var ids []string
	if err := decodeJSON(request, &ids, 64<<10); err != nil || len(ids) == 0 {
		writeError(response, http.StatusBadRequest, "invalid address book peer list")
		return
	}
	entries, err := s.repository.ListAddressBookEntries(request.Context(), bookID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "address book unavailable")
		return
	}
	byRustDeskID := make(map[string]string, len(entries))
	for _, entry := range entries {
		byRustDeskID[entry.RustDeskID] = entry.ID
	}
	for _, rustDeskID := range ids {
		entryID, ok := byRustDeskID[rustDeskID]
		if !ok {
			writeError(response, http.StatusNotFound, "address book peer not found")
			return
		}
		if err := s.repository.DeleteAddressBookEntry(request.Context(), bookID, entryID); err != nil {
			writeError(response, statusForStore(err), "address book peer not found")
			return
		}
	}
	response.WriteHeader(http.StatusOK)
}

func (s *Server) personalAddressBook(request *http.Request, user domain.User, create bool) (domain.AddressBook, bool, error) {
	books, err := s.repository.ListAddressBooks(request.Context())
	if err != nil {
		return domain.AddressBook{}, false, err
	}
	for _, book := range books {
		if book.OwnerUserID == user.ID && book.Kind == "personal" {
			book.Permission, book.CanManage = addressbookservice.PermissionManage, true
			return book, true, nil
		}
	}
	if !create {
		return domain.AddressBook{}, false, nil
	}
	now := time.Now().UTC()
	book := domain.AddressBook{ID: uuid.NewString(), Name: user.Username, Kind: "personal", OwnerUserID: user.ID,
		Permission: addressbookservice.PermissionManage, CanManage: true, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateAddressBook(request.Context(), book); err != nil {
		return domain.AddressBook{}, false, err
	}
	return book, true, nil
}

func entriesFromClientPeers(bookID string, peers []rustDeskAddressBookPeer) ([]domain.AddressBookEntry, bool) {
	now := time.Now().UTC()
	seen := make(map[string]struct{}, len(peers))
	entries := make([]domain.AddressBookEntry, 0, len(peers))
	for _, peer := range peers {
		peer.ID, peer.Alias = strings.TrimSpace(peer.ID), strings.TrimSpace(peer.Alias)
		if len(peer.ID) < 3 || len(peer.ID) > 64 || len(peer.Alias) > 128 {
			return nil, false
		}
		if _, exists := seen[peer.ID]; exists {
			return nil, false
		}
		seen[peer.ID] = struct{}{}
		entries = append(entries, domain.AddressBookEntry{ID: uuid.NewString(), AddressBookID: bookID, RustDeskID: peer.ID, Alias: peer.Alias,
			Username: strings.TrimSpace(peer.Username), Hostname: strings.TrimSpace(peer.Hostname), Platform: strings.TrimSpace(peer.Platform),
			Tags: normalizeAddressBookTags(peer.Tags), ForceRelay: clientBool(peer.ForceAlwaysRelay), RDPPort: strings.TrimSpace(peer.RDPPort),
			RDPUsername: strings.TrimSpace(peer.RDPUsername), LoginName: strings.TrimSpace(peer.LoginName), SameServer: peer.SameServer, CreatedAt: now})
	}
	return entries, true
}

func clientPeers(entries []domain.AddressBookEntry) []rustDeskAddressBookPeer {
	peers := make([]rustDeskAddressBookPeer, 0, len(entries))
	for _, entry := range entries {
		peers = append(peers, rustDeskAddressBookPeer{ID: entry.RustDeskID, Alias: entry.Alias, Username: entry.Username,
			Hostname: entry.Hostname, Platform: entry.Platform, Tags: entry.Tags, ForceAlwaysRelay: strconv.FormatBool(entry.ForceRelay),
			RDPPort: entry.RDPPort, RDPUsername: entry.RDPUsername, LoginName: entry.LoginName, SameServer: entry.SameServer})
	}
	return peers
}

func clientBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		value, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && value
	default:
		return false
	}
}

func normalizeAddressBookTags(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validAddressBookTag(value) {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func clientPermissionRule(permission string) int {
	switch permission {
	case addressbookservice.PermissionRead:
		return 1
	case addressbookservice.PermissionWrite:
		return 2
	default:
		return 3
	}
}
