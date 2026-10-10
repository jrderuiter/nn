package nono

import (
	"encoding/json"
	"testing"
)

func TestCondPathMarshal(t *testing.T) {
	cases := []struct {
		name string
		in   CondPath
		want string
	}{
		{"bare", P("$HOME/.cache"), `"$HOME/.cache"`},
		{"single when", PWhen("/opt/homebrew", "macos"), `{"path":"/opt/homebrew","when":"macos"}`},
		{"many when", PWhen("/usr/lib", "linux", "macos"), `{"path":"/usr/lib","when":["linux","macos"]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
			var back CondPath
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			if back.Path != c.in.Path || len(back.When) != len(c.in.When) {
				t.Fatalf("round trip lost data: %+v", back)
			}
		})
	}
}

func TestDomainMarshal(t *testing.T) {
	bare, _ := json.Marshal(Domain{Domain: "api.github.com"})
	if string(bare) != `"api.github.com"` {
		t.Fatalf("bare domain: got %s", bare)
	}
	withRules, _ := json.Marshal(Domain{
		Domain:    "api.github.com",
		Endpoints: []Endpoint{{Method: "GET", Path: "/user"}},
	})
	want := `{"domain":"api.github.com","endpoints":[{"method":"GET","path":"/user"}]}`
	if string(withRules) != want {
		t.Fatalf("scoped domain: got %s, want %s", withRules, want)
	}
}

func TestEmptyWhenArrayIsRejected(t *testing.T) {
	var c CondPath
	err := json.Unmarshal([]byte(`{"path":"/x","when":[]}`), &c)
	if err == nil {
		t.Fatal("an empty when array must be an error, because nono rejects it")
	}
}

// A profile with no content must marshal to an empty object. Any stray null
// key is a hard error for nono, whose top level sets additionalProperties
// false.
func TestEmptyProfileHasNoKeys(t *testing.T) {
	got, err := json.Marshal(&Profile{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Fatalf("got %s, want {}", got)
	}
}

func TestLinuxMarshal(t *testing.T) {
	got, err := json.Marshal(&Profile{Linux: &Linux{AfUnixMediation: "pathname"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"linux":{"af_unix_mediation":"pathname"}}`; string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	empty, _ := json.Marshal(&Linux{})
	if string(empty) != "{}" {
		t.Fatalf("an unset mode must be left out, got %s", empty)
	}
}
