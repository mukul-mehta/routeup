package server

import "testing"

func TestDeriveTokenHost(t *testing.T) {
	cases := []struct {
		route string
		base  string
		want  string
	}{
		{route: "myapp", base: "routeup.dev", want: "myapp.routeup.dev"},
		{route: "api.myapp", base: "alice.routeup.dev", want: "api.myapp.alice.routeup.dev"},
		{route: "API.MyApp", base: "Alice.RouteUp.Dev", want: "api.myapp.alice.routeup.dev"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := DeriveTokenHost(tc.route, tc.base); got != tc.want {
				t.Errorf("DeriveTokenHost(%q, %q) = %q, want %q", tc.route, tc.base, got, tc.want)
			}
		})
	}
}

func TestDeriveNamespaceHost(t *testing.T) {
	if got := DeriveNamespaceHost("foo", "try", "routeup.dev"); got != "foo.try.routeup.dev" {
		t.Errorf("DeriveNamespaceHost = %q, want %q", got, "foo.try.routeup.dev")
	}
}

func TestImmediateChildLabel(t *testing.T) {
	cases := []struct {
		host   string
		domain string
		want   string
		ok     bool
	}{
		{host: "api.myapp.routeup.dev", domain: "routeup.dev", want: "myapp", ok: true},
		{host: "api.routeup.dev", domain: "routeup.dev", want: "api", ok: true},
		{host: "foo.try.routeup.dev", domain: "routeup.dev", want: "try", ok: true},
		{host: "myapp.routeup.dev", domain: "routeup.dev", want: "myapp", ok: true},
		{host: "API.Alice.RouteUp.Dev", domain: "routeup.dev", want: "alice", ok: true},
		{host: "routeup.dev", domain: "routeup.dev", want: "", ok: false},
		{host: "other.com", domain: "routeup.dev", want: "", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			got, ok := ImmediateChildLabel(tc.host, tc.domain)
			if got != tc.want || ok != tc.ok {
				t.Errorf("ImmediateChildLabel(%q, %q) = (%q, %v), want (%q, %v)",
					tc.host, tc.domain, got, ok, tc.want, tc.ok)
			}
		})
	}
}
