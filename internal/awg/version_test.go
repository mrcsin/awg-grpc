package awg

import "testing"

func TestParseToolsVersion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "pinned tools",
			input: "amneziawg-tools v3.1.20260812 - https://amnezia.org\n",
			want:  "3.1.20260812",
		},
		{
			name:  "git describe suffix",
			input: "amneziawg-tools v3.1.20260812-3-gee0f0a9-dirty - https://amnezia.org\n",
			want:  "3.1.20260812-3-gee0f0a9-dirty",
		},
		{
			name:  "no trailing newline",
			input: "amneziawg-tools v3.0.20260730 - https://amnezia.org",
			want:  "3.0.20260730",
		},
		{name: "missing prefix", input: "wireguard-tools v1.0.20210914 - https://git.zx2c4.com/wireguard-tools/\n", wantErr: true},
		{name: "empty version", input: "amneziawg-tools v - https://amnezia.org\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseToolsVersion([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseToolsVersion = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseToolsVersion: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseToolsVersion = %q, want %q", got, tt.want)
			}
		})
	}
}
