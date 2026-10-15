package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestWriteFlagsIncludesDeprecatedShorthand(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.StringP("filename", "f", "", "file to read")
	flags.Lookup("filename").ShorthandDeprecated = "use --filename instead"

	var got bytes.Buffer
	writeFlags(&got, flags)

	if !strings.Contains(got.String(), `ShorthandDeprecated: "use --filename instead"`) {
		t.Fatalf("writeFlags() = %q, want the shorthand deprecation message", got.String())
	}
}
