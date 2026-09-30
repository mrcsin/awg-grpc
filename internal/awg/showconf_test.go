package awg

import (
	"reflect"
	"strings"
	"testing"
)

func defaultHeaderParams() []Param {
	return []Param{{"H1", "1"}, {"H2", "2"}, {"H3", "3"}, {"H4", "4"}}
}

func TestParseClientParamsFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		want    []Param
	}{
		{
			fixture: "full.conf",
			want: []Param{
				{"Jc", "4"},
				{"Jmin", "40"},
				{"Jmax", "70"},
				{"S1", "15"},
				{"S2", "18"},
				{"S3", "20"},
				{"S4", "23"},
				{"H1", "100000-199999"},
				{"H2", "200000-299999"},
				{"H3", "300000-399999"},
				{"H4", "400000-499999"},
				{"I1", "<b 0xc6000000010801> <r 16> <c> <t>"},
				{"I2", "<r 32>"},
				{"I3", "<rc 8>"},
				{"I4", "<rd 4>"},
				{"I5", "<b 0xdeadbeef>"},
				{"HeaderProtectionKey", "kGvokoudkDy4Oc9m8UeLE8OBSKrhbNhxOLydN5Np6Vw="},
				{"ContentPaddingAddition", "4-16"},
				{"RekeyAfterTime", "110"},
				{"RekeyTimeout", "4"},
				{"RejectAfterTime", "170"},
				{"KeepaliveTimeout", "9"},
				{"MaxHandshakeAttempts", "15"},
				{"RandomTrailers", "on"},
				{"DisableCookies", "on"},
			},
		},
		{fixture: "no-peers.conf", want: defaultHeaderParams()},
		{fixture: "no-private-key.conf", want: defaultHeaderParams()},
		{fixture: "psk-mixed.conf", want: defaultHeaderParams()},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			got, err := ParseClientParams(readFixture(t, "showconf", tt.fixture))
			if err != nil {
				t.Fatalf("ParseClientParams: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseClientParams mismatch\n got: %v\nwant: %v", got, tt.want)
			}
		})
	}
}

func TestParseClientParams(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Param
	}{
		{
			name:  "order kept",
			input: "[Interface]\nS2 = 18\nJc = 4\nS1 = 15\n",
			want:  []Param{{"S2", "18"}, {"Jc", "4"}, {"S1", "15"}},
		},
		{
			name: "private key, listen port and fwmark dropped",
			input: "[Interface]\nListenPort = 51820\nFwMark = 0x1234\n" +
				"PrivateKey = 8AJv62BeaH/5/fetnh3CJdj5ybdnew3xa5eX28LjhHM=\nJc = 4\n",
			want: []Param{{"Jc", "4"}},
		},
		{
			name:  "server-only keys dropped in any case",
			input: "[Interface]\nprivatekey = 8AJv62BeaH/5/fetnh3CJdj5ybdnew3xa5eX28LjhHM=\nLISTENPORT = 1\nfwmark = 0x1\nJc = 4\n",
			want:  []Param{{"Jc", "4"}},
		},
		{
			name:  "zero and off dropped",
			input: "[Interface]\nJc = 0\nS1 = 15\nRandomTrailers = off\nDisableCookies = on\n",
			want:  []Param{{"S1", "15"}, {"DisableCookies", "on"}},
		},
		{
			name:  "header range kept",
			input: "[Interface]\nH1 = 100-200\n",
			want:  []Param{{"H1", "100-200"}},
		},
		{
			name:  "expression with spaces kept verbatim",
			input: "[Interface]\nI1 = <b 0xc6000000010801> <r 16> <c> <t>\n",
			want:  []Param{{"I1", "<b 0xc6000000010801> <r 16> <c> <t>"}},
		},
		{
			name:  "base64 value with padding kept",
			input: "[Interface]\nHeaderProtectionKey = kGvokoudkDy4Oc9m8UeLE8OBSKrhbNhxOLydN5Np6Vw=\n",
			want:  []Param{{"HeaderProtectionKey", "kGvokoudkDy4Oc9m8UeLE8OBSKrhbNhxOLydN5Np6Vw="}},
		},
		{
			name: "parsing stops at the first peer",
			input: "[Interface]\nJc = 4\n\n[Peer]\nPublicKey = CY4TOiryEYeopArmdKp/sa61ESKWl1Kbgje4rbFbUHw=\n" +
				"weird key = x\n",
			want: []Param{{"Jc", "4"}},
		},
		{
			name:  "interface without parameters",
			input: "[Interface]\nListenPort = 51820\nJc = 0\n",
			want:  []Param{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseClientParams([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseClientParams: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseClientParams mismatch\n got: %v\nwant: %v", got, tt.want)
			}
		})
	}
}

func TestParseClientParamsMalformed(t *testing.T) {
	const marker = "LEAKMARK"

	tests := []struct {
		name     string
		input    string
		wantText []string
	}{
		{
			name:     "empty output",
			input:    "",
			wantText: []string{"[Interface]"},
		},
		{
			name:     "no interface section",
			input:    "Jc = " + marker + "\n",
			wantText: []string{"line 1", "[Interface]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseClientParams([]byte(tt.input))
			if err == nil {
				t.Fatal("ParseClientParams succeeded, want an error")
			}
			msg := err.Error()
			if strings.Contains(msg, marker) {
				t.Fatalf("error quotes the input: %s", msg)
			}
			for _, want := range tt.wantText {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q lacks %q", msg, want)
				}
			}
		})
	}
}
