package provider

import (
	"testing"
)

func TestCanonicalJSONMatchesJSONEncode(t *testing.T) {
	cases := map[string]string{
		`{ "zeta": 1, "alpha": { "b": true, "a": null } }`: `{"alpha":{"a":null,"b":true},"zeta":1}`,
		`{"tiny":5e-7}`:                    `{"tiny":0.0000005}`,
		`{"decimal":1.5,"negative":-0.25}`: `{"decimal":1.5,"negative":-0.25}`,
		`{"account":123456789012}`:         `{"account":123456789012}`,
		`{"exact":9007199254740993}`:       `{"exact":9007199254740993}`,
		`{"list":[3,2.5e-8,"text"]}`:       `{"list":[3,0.000000025,"text"]}`,
		// jsonencode() escapes these characters the same way.
		`{"html":"<b>&</b>"}`: `{"html":"` + "\x5cu003cb\x5cu003e\x5cu0026\x5cu003c/b\x5cu003e" + `"}`,
		`not json`:            `not json`,
	}
	for input, want := range cases {
		if got := canonicalJSON([]byte(input)); got != want {
			t.Errorf("canonicalJSON(%s) = %s, want %s", input, got, want)
		}
	}
}

func TestJSONValuesEqualComparesNumbersAsTheAPIStoresThem(t *testing.T) {
	equal := [][2]string{
		{`{"tiny":0.0000005}`, `{"tiny":5e-7}`},
		{`{"precise":0.12345678901234567}`, `{"precise":0.12345678901234566}`},
		{`{"a":1,"b":[1.50]}`, `{ "b": [1.5], "a": 1.0 }`},
	}
	for _, pair := range equal {
		if !jsonValuesEqual(pair[0], pair[1]) {
			t.Errorf("jsonValuesEqual(%s, %s) = false, want true", pair[0], pair[1])
		}
	}

	different := [][2]string{
		{`{"a":1}`, `{"a":2}`},
		{`{"a":1}`, `{"a":"1"}`},
		{`{"a":[1,2]}`, `{"a":[2,1]}`},
		{`{"a":1}`, `{"a":1,"b":null}`},
	}
	for _, pair := range different {
		if jsonValuesEqual(pair[0], pair[1]) {
			t.Errorf("jsonValuesEqual(%s, %s) = true, want false", pair[0], pair[1])
		}
	}
}

func TestFindUnsafeInteger(t *testing.T) {
	cases := map[string]bool{
		`{"safe":9007199254740991}`:         false,
		`{"limit":9007199254740992}`:        true,
		`{"nested":{"list":[1,2,1e20]}}`:    true,
		`{"negative":-9007199254740993}`:    true,
		`{"fraction":9007199254740993.5}`:   true,
		`{"small":0.0000005,"text":"1e30"}`: false,
	}
	for input, want := range cases {
		var decoded any
		if err := decodeWithNumbers([]byte(input), &decoded); err != nil {
			t.Fatalf("decoding %s: %v", input, err)
		}
		if _, got := findUnsafeInteger(decoded); got != want {
			t.Errorf("findUnsafeInteger(%s) = %v, want %v", input, got, want)
		}
	}
}
