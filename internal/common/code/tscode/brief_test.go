package tscode

import "testing"

// TestBrief: interface/type member lists disappear, but the enum's members
// (already just bare case names, no more detail than a name list) and every
// type's kind/name/exported survive.
func TestBrief(t *testing.T) {
	f := readTS(t, "auth.ts", sampleTS)
	f.Brief()

	ci := findType(f, "CurrentUser")
	if ci == nil || ci.Kind != "interface" || ci.Members != "" {
		t.Errorf("CurrentUser after Brief = %+v, want empty members", ci)
	}
	if a := findType(f, "Id"); a == nil || a.Kind != "type" || a.Members != "" {
		t.Errorf("type alias Id after Brief = %+v, want empty members", a)
	}
	if e := findType(f, "DealStatus"); e == nil || e.Kind != "enum" || e.Members != "Open, Won, Lost" {
		t.Errorf("enum DealStatus after Brief = %+v, want members kept", e)
	}
}
