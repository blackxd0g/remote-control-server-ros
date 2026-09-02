package httpapi

import "testing"

func TestSpreadsheetSafePreventsFormulaInjection(t *testing.T) {
	for _, value := range []string{"=cmd()", "+1", "-2", "@SUM(A1)"} {
		if got := spreadsheetSafe(value); got != "'"+value {
			t.Fatalf("value %q was not protected: %q", value, got)
		}
	}
	if got := spreadsheetSafe("normal"); got != "normal" {
		t.Fatalf("normal value changed: %q", got)
	}
}

func TestParseDeviceManagementCSV(t *testing.T) {
	values, err := parseDeviceManagementCSV([]byte("\xef\xbb\xbfrustdesk_id;alias;group_id;tags\n123456;Office PC;;office, managed\n"))
	if err != nil || len(values) != 1 {
		t.Fatalf("parse failed: values=%#v err=%v", values, err)
	}
	if values[0].RustDeskID != "123456" || values[0].Alias != "Office PC" || len(values[0].Tags) != 2 {
		t.Fatalf("unexpected value: %#v", values[0])
	}
}

func TestParseDeviceManagementCSVRejectsDuplicates(t *testing.T) {
	_, err := parseDeviceManagementCSV([]byte("rustdesk_id,alias,group_id,tags\n123,a,,\n123,b,,\n"))
	if err == nil {
		t.Fatal("duplicate ID was accepted")
	}
}

func TestParseDeviceManagementCSVRejectsMissingColumns(t *testing.T) {
	_, err := parseDeviceManagementCSV([]byte("rustdesk_id;alias\n123;pc\n"))
	if err == nil {
		t.Fatal("incomplete header was accepted")
	}
}
