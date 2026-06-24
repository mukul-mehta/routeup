package server

import "testing"

func TestNewReservedSet_Defaults(t *testing.T) {
	s := NewReservedSet()
	for _, l := range []string{"api", "admin", "app", "dashboard", "dev", "docs", "status", "www", "try"} {
		if !s.Has(l) {
			t.Errorf("default set missing reserved label %q", l)
		}
	}
	if s.Has("myapp") {
		t.Errorf("did not expect %q to be reserved", "myapp")
	}
}

func TestNewReservedSet_Extra(t *testing.T) {
	s := NewReservedSet("billing", "  ", "TRY")
	if !s.Has("billing") {
		t.Errorf("expected extra label %q to be reserved", "billing")
	}
	if !s.Has("try") {
		t.Errorf("expected %q to remain reserved", "try")
	}
	if !s.Has("api") {
		t.Errorf("expected defaults to remain when extras are added")
	}
}

func TestReservedSet_Has_CaseInsensitive(t *testing.T) {
	s := NewReservedSet()
	for _, in := range []string{"API", " api ", "Api"} {
		if !s.Has(in) {
			t.Errorf("Has(%q) = false, want true", in)
		}
	}
	if s.Has("") {
		t.Errorf("Has(\"\") = true, want false")
	}
}
