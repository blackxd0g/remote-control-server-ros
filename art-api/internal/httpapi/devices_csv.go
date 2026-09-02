package httpapi

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

const (
	maximumDeviceCSVSize = int64(8 << 20)
	maximumDeviceCSVRows = 5000
)

func (s *Server) exportDevicesCSV(response http.ResponseWriter, request *http.Request) {
	devices, err := s.repository.ListDevices(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "devices unavailable")
		return
	}
	response.Header().Set("Content-Type", "text/csv; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="rustdesk-devices.csv"`)
	response.Header().Set("Cache-Control", "no-store")
	_, _ = response.Write([]byte{0xef, 0xbb, 0xbf})
	writer := csv.NewWriter(response)
	writer.Comma = ';'
	_ = writer.Write([]string{"rustdesk_id", "hostname", "alias", "platform", "version", "username", "last_seen_ip", "online", "last_seen", "owner_user_id", "group_id", "tags", "deployed", "archived_at", "created_at"})
	for _, device := range devices {
		archivedAt := ""
		if device.ArchivedAt != nil {
			archivedAt = device.ArchivedAt.UTC().Format(timeLayout)
		}
		_ = writer.Write([]string{
			spreadsheetSafe(device.RustDeskID), spreadsheetSafe(device.Hostname), spreadsheetSafe(device.Alias),
			spreadsheetSafe(device.Platform), spreadsheetSafe(device.Version), spreadsheetSafe(device.Username),
			spreadsheetSafe(device.LastSeenIP), strconv.FormatBool(device.Online), device.LastSeen.UTC().Format(timeLayout),
			spreadsheetSafe(device.OwnerUserID), spreadsheetSafe(device.GroupID), spreadsheetSafe(strings.Join(device.Tags, ",")),
			strconv.FormatBool(device.Deployed), archivedAt, device.CreatedAt.UTC().Format(timeLayout),
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return
	}
}

const timeLayout = "2006-01-02T15:04:05Z07:00"

func spreadsheetSafe(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
		return "'" + value
	}
	return value
}

func (s *Server) importDevicesCSV(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if request.ContentLength > maximumDeviceCSVSize {
		writeError(response, http.StatusRequestEntityTooLarge, "device CSV is too large")
		return
	}
	payload, err := io.ReadAll(io.LimitReader(request.Body, maximumDeviceCSVSize+1))
	if err != nil || len(payload) == 0 || int64(len(payload)) > maximumDeviceCSVSize {
		writeError(response, http.StatusBadRequest, "invalid device CSV")
		return
	}
	values, err := parseDeviceManagementCSV(payload)
	if err == nil {
		groups, groupErr := s.repository.ListGroups(request.Context())
		if groupErr != nil {
			err = groupErr
		} else {
			deviceGroups := make(map[string]bool, len(groups))
			for _, group := range groups {
				if group.Kind == domain.GroupKindDevice {
					deviceGroups[group.ID] = true
				}
			}
			for _, value := range values {
				if value.GroupID != "" && !deviceGroups[value.GroupID] {
					err = fmt.Errorf("unknown device group %q", value.GroupID)
					break
				}
			}
		}
	}
	if err != nil {
		_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_import", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "denied", Reason: "invalid_csv"})
		writeError(response, http.StatusUnprocessableEntity, "invalid device CSV: "+err.Error())
		return
	}
	if err = s.repository.ImportDeviceManagement(request.Context(), values); err != nil {
		status, message := http.StatusInternalServerError, "device import failed"
		if errors.Is(err, domain.ErrNotFound) {
			status, message = http.StatusNotFound, "CSV contains an unknown or archived device"
		}
		_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_import", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "denied", Reason: "apply_failed", Metadata: map[string]any{"records": len(values)}})
		writeError(response, status, message)
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_import", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"records": len(values)}})
	writeJSON(response, http.StatusOK, map[string]any{"imported": len(values)})
}

func parseDeviceManagementCSV(payload []byte) ([]domain.DeviceManagementImport, error) {
	payload = bytes.TrimPrefix(payload, []byte{0xef, 0xbb, 0xbf})
	firstLine := string(payload)
	if index := strings.IndexByte(firstLine, '\n'); index >= 0 {
		firstLine = firstLine[:index]
	}
	reader := csv.NewReader(bytes.NewReader(payload))
	if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
		reader.Comma = ';'
	}
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 {
		return nil, errors.New("header and at least one data row are required")
	}
	headings := make(map[string]int, len(records[0]))
	for index, heading := range records[0] {
		headings[strings.ToLower(strings.TrimSpace(heading))] = index
	}
	for _, required := range []string{"rustdesk_id", "alias", "group_id", "tags"} {
		if _, ok := headings[required]; !ok {
			return nil, fmt.Errorf("missing %s column", required)
		}
	}
	if len(records)-1 > maximumDeviceCSVRows {
		return nil, fmt.Errorf("more than %d records", maximumDeviceCSVRows)
	}
	valueAt := func(record []string, name string) string {
		index := headings[name]
		if index >= len(record) {
			return ""
		}
		return strings.TrimSpace(strings.ReplaceAll(record[index], "\x00", ""))
	}
	seen := make(map[string]bool, len(records)-1)
	values := make([]domain.DeviceManagementImport, 0, len(records)-1)
	for row, record := range records[1:] {
		id, alias, groupID := valueAt(record, "rustdesk_id"), valueAt(record, "alias"), valueAt(record, "group_id")
		if id == "" && alias == "" && groupID == "" && valueAt(record, "tags") == "" {
			continue
		}
		if len(id) < 3 || len(id) > 64 || seen[id] {
			return nil, fmt.Errorf("invalid or duplicate rustdesk_id at row %d", row+2)
		}
		if len(alias) > 128 || len(groupID) > 128 {
			return nil, fmt.Errorf("field too long at row %d", row+2)
		}
		tags := make([]string, 0)
		tagSeen := map[string]bool{}
		for _, tag := range strings.Split(valueAt(record, "tags"), ",") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if len(tag) > 64 || len(tags) >= 32 {
				return nil, fmt.Errorf("invalid tags at row %d", row+2)
			}
			if !tagSeen[tag] {
				tags, tagSeen[tag] = append(tags, tag), true
			}
		}
		seen[id] = true
		values = append(values, domain.DeviceManagementImport{RustDeskID: id, Alias: alias, GroupID: groupID, Tags: tags})
	}
	if len(values) == 0 {
		return nil, errors.New("no device records")
	}
	return values, nil
}
