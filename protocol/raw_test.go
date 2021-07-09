package protocol

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func hexCmp(got []byte, want string) string {
	gotS := hex.EncodeToString(got)
	if gotS != want {
		return fmt.Sprintf("got=%s want=%s", gotS, want)
	}
	return ""
}

func TestXorMessage(t *testing.T) {
	tests := []struct {
		in, want string
		xor      byte
	}{
		{
			in:   "d8b2b7a9b7b725",
			xor:  0xb7,
			want: "6f05001e000092",
		},
	}

	for _, test := range tests {
		t.Run(test.in, func(t *testing.T) {
			in, err := hex.DecodeString(test.in)
			if err != nil {
				t.Fatal(err)
			}
			got := XorMessageWith(in, test.xor)
			if diff := hexCmp(got, test.want); diff != "" {
				t.Errorf(diff)
			}
		})
	}
}

func TestValidateChecksum(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{
			in:   "bb04016cf01c",
			want: true,
		}, {
			in:   "f60400060303",
			want: true,
		}, {
			in:   "f60400060304",
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.in, func(t *testing.T) {
			in, err := hex.DecodeString(test.in)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := ValidateChecksum(in), test.want; got != want {
				t.Fatalf("got=%v want=%v", got, want)
			}
		})
	}
}
func TestValidateAndDecodeMessage(t *testing.T) {
	tests := []struct {
		in, want, remaining string
		xor                 byte
	}{
		{
			in:        "06f4f0f6f3f306f4f0f6f3f3",
			want:      "f60400060303",
			remaining: "06f4f0f6f3f3",
			xor:       0xf0,
		}, {
			in:        "ff879094eda82091132d9091ece0a891906f6f93906f6f93c8",
			want:      "6f1700047d38b00183bd00017c70380100ffff0300ffff0358",
			remaining: "",
			xor:       0x90,
		}, {
			in:        "d8b2b7a9b7b725",
			want:      "6f05001e000092",
			remaining: "",
			xor:       0xb7,
		}, {
			in:        "4ab8bd95bc98",
			want:      "f60401290024",
			remaining: "",
			xor:       0xbc,
		}, {
			in:        "f20a000100000000000000fd",
			want:      "f20a000100000000000000fd",
			remaining: "",
			xor:       0x00,
		}, {
			in:        "502f3fff0f0f0a0d0f0d0d0f0f0f2f3e3f04",
			want:      "6f1000c0303035323032323030301001003b",
			remaining: "",
			xor:       0x3f,
		}, {
			in:        "3cf4f16e55e4",
			want:      "cc04019ea514",
			remaining: "",
			xor:       0xf0,
		}, {
			in:        "06f4f0f6f3f3",
			want:      "f60400060303",
			remaining: "",
			xor:       0xf0,
		}, {
			in:        "4bf4f19c00ec",
			want:      "bb04016cf01c",
			remaining: "",
			xor:       0xf0,
		},
	}

	for _, test := range tests {
		t.Run(test.in, func(t *testing.T) {
			in, err := hex.DecodeString(test.in)
			if err != nil {
				t.Fatal(err)
			}
			got, xor, gotRem := ValidateAndDecodeMessage(in)
			if gs := hex.EncodeToString(got); gs != test.want {
				t.Errorf("got=%s want=%s", gs, test.want)
			}
			if gs := hex.EncodeToString(gotRem); gs != test.remaining {
				t.Errorf("gotRem=%s want=%s", gs, test.remaining)
			}
			if xor != test.xor {
				t.Errorf("Xor got=%x want=%x", xor, test.xor)
			}
		})
	}
}
func TestDecodeMessages(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{
			in:   "caa2a5a7a5a5a5a5dd4bf4f1f15596",
			want: "6f0700020000000078,bb040101a566",
		}, {
			in:   "06f4f0f6f3f306f4f0f6f3f3",
			want: "f60400060303,f60400060303",
		}, {
			in:   "ff879094eda82091132d9091ece0a891906f6f93906f6f93c8",
			want: "6f1700047d38b00183bd00017c70380100ffff0300ffff0358",
		},
	}

	for _, test := range tests {
		t.Run(test.in, func(t *testing.T) {
			in, err := hex.DecodeString(test.in)
			if err != nil {
				t.Fatal(err)
			}
			got := GetDecodedMessages(in)
			gotList := []string{}
			for _, m := range got {
				gotList = append(gotList, hex.EncodeToString(m))
			}
			gotStr := strings.Join(gotList, ",")
			if gotStr != test.want {
				t.Errorf("got=%s want=%s", gotStr, test.want)
			}
		})
	}
}
