package update

import "testing"

func TestDetectChannel(t *testing.T) {
	cases := []struct {
		path string
		want Channel
	}{
		{"/opt/homebrew/Cellar/routeup/0.1.0/bin/routeup", ChannelHomebrew},
		{"/usr/local/Cellar/routeup/0.1.0/bin/routeup", ChannelHomebrew},
		{"/home/linuxbrew/.linuxbrew/Cellar/routeup/0.1.0/bin/routeup", ChannelHomebrew},
		{"/usr/local/bin/routeup", ChannelDirect},
		{"/home/me/.local/bin/routeup", ChannelDirect},
		{"/Users/me/.local/bin/routeup", ChannelDirect},
	}
	for _, tc := range cases {
		if got := DetectChannel(tc.path); got != tc.want {
			t.Errorf("DetectChannel(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		name            string
		current, latest string
		want            bool
		wantErr         bool
	}{
		{"older", "0.1.0", "v0.2.0", true, false},
		{"equal", "0.2.0", "v0.2.0", false, false},
		{"newer-local", "0.3.0", "v0.2.0", false, false},
		{"patch", "v0.1.0", "v0.1.1", true, false},
		{"with-v-both", "v1.0.0", "v1.0.0", false, false},
		{"bad-current", "garbage", "v0.1.0", false, true},
		{"bad-latest", "0.1.0", "not-a-tag", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IsNewer(tc.current, tc.latest)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for current=%q latest=%q", tc.current, tc.latest)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("IsNewer(%q,%q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}
