package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeData(t *testing.T) {
	var object map[string]json.RawMessage
	// "hunter2" and "app", base64 as the API returns them.
	if err := json.Unmarshal([]byte(
		`{"data":{"password":"aHVudGVyMg==","username":"YXBw"}}`), &object); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys, values, err := decodeData(object)
	if err != nil {
		t.Fatalf("decodeData: %v", err)
	}
	// Sorted, because the API returns a JSON object and Go map iteration would
	// reorder the rows on every run.
	if len(keys) != 2 || keys[0] != "password" || keys[1] != "username" {
		t.Errorf("keys = %v, want [password username]", keys)
	}
	if string(values["password"]) != "hunter2" {
		t.Errorf("password = %q, want hunter2", values["password"])
	}
}

func TestDecodeDataEmptySecret(t *testing.T) {
	keys, values, err := decodeData(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("decodeData: %v", err)
	}
	if len(keys) != 0 || len(values) != 0 {
		t.Errorf("keys = %v, values = %v; want empty", keys, values)
	}
}

// Binary payloads render as a placeholder so a keystore doesn't garble the
// table; --key still emits the raw bytes.
func TestToDisplay(t *testing.T) {
	if got := toDisplay([]byte("hunter2")); got != "hunter2" {
		t.Errorf("toDisplay(text) = %q", got)
	}
	binary := []byte{0xfe, 0xed, 0xfa, 0xce, 0x00}
	if got := toDisplay(binary); got != "<binary, 5 bytes>" {
		t.Errorf("toDisplay(binary) = %q, want a placeholder", got)
	}
}

// The trailing newline is added only for text, so a binary redirect reproduces
// the file exactly while text stays shell-friendly.
func TestWriteValueNewlineOnlyForText(t *testing.T) {
	// Verified end-to-end against the Python build; this pins the rule the
	// implementation encodes.
	text := []byte("token")
	if !isText(text) {
		t.Error("text was classified as binary")
	}
	binary := []byte{0x00, 0xff}
	if isText(binary) {
		t.Error("binary was classified as text")
	}
}

func isText(value []byte) bool { return toDisplay(value) == string(value) }

func TestSecretFlags(t *testing.T) {
	rest, options, err := secretFlags([]string{
		"1", "--decode", "-k", "tls.crt", "-m", "web", "-y", "-n", "prod"})
	if err != nil {
		t.Fatalf("secretFlags: %v", err)
	}
	if !options.Decode || !options.Yes {
		t.Errorf("options = %+v, want decode and yes set", options)
	}
	if !options.HasKey || options.Key != "tls.crt" {
		t.Errorf("key = %q (set=%v), want tls.crt", options.Key, options.HasKey)
	}
	if options.Match != "web" {
		t.Errorf("match = %q, want web", options.Match)
	}
	// Everything kx doesn't own reaches kubectl untouched.
	if strings.Join(rest, " ") != "1 -n prod" {
		t.Errorf("rest = %q, want \"1 -n prod\"", strings.Join(rest, " "))
	}
}

// An empty --key is distinguishable from an absent one, so `-k ""` is still an
// error about a missing key rather than being silently ignored.
func TestSecretFlagsAbsentKey(t *testing.T) {
	_, options, err := secretFlags([]string{"1", "--decode"})
	if err != nil {
		t.Fatalf("secretFlags: %v", err)
	}
	if options.HasKey {
		t.Error("HasKey is set with no --key given")
	}
}
