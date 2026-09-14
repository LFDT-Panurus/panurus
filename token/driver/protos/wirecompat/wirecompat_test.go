/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package wirecompat contains tests that demonstrate why Panurus gates its
// protobuf definitions with `buf breaking` (see buf.yaml and
// docs/upgradability.md).
//
// The danger these tests illustrate is that a wire-incompatible edit to a
// message does NOT make proto.Unmarshal fail. The old bytes decode without
// error, but the payload silently lands in the wrong place — the target field
// comes back empty and the original value is retained only as an unreadable
// unknown field. A node running the new schema therefore loses data written by
// a node running the old schema, with no runtime signal at all.
//
// TestWireBreakingChangesSilentlyCorruptRoundTrip proves that silent
// corruption at the marshal/unmarshal layer. TestBufBreakingGateCatchesWireBreaks
// proves that the CI gate catches exactly those edits before they can merge —
// which is the only place they *can* be caught, since the runtime does not.
package wirecompat

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// field is a minimal field spec used to synthesise a message schema at runtime.
type field struct {
	name string
	num  int32
	typ  descriptorpb.FieldDescriptorProto_Type
}

const (
	tUint64 = descriptorpb.FieldDescriptorProto_TYPE_UINT64
	tString = descriptorpb.FieldDescriptorProto_TYPE_STRING
)

// schema builds a "demo.v1.Token" message descriptor from the given fields, so a
// test can stand up an "old" and a "new" version of the same message and
// exchange bytes between them exactly as two node versions would.
func schema(t *testing.T, fields ...field) protoreflect.MessageDescriptor {
	t.Helper()
	fdps := make([]*descriptorpb.FieldDescriptorProto, len(fields))
	for i, f := range fields {
		fdps[i] = &descriptorpb.FieldDescriptorProto{
			Name:   new(f.name),
			Number: new(f.num),
			Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:   f.typ.Enum(),
		}
	}
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:        new("demo/v1/token.proto"),
		Syntax:      new("proto3"),
		Package:     new("demo.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: new("Token"), Field: fdps}},
	}, nil)
	if err != nil {
		t.Fatalf("build descriptor: %v", err)
	}
	return fd.Messages().Get(0)
}

// TestWireBreakingChangesSilentlyCorruptRoundTrip marshals a Token under a
// baseline schema and unmarshals the bytes under an edited schema, mimicking a
// new-version node reading an old-version node's message. It asserts that
// wire-breaking edits silently drop the payload (no error, empty field, value
// stranded in unknown fields), while a wire-safe rename preserves it.
func TestWireBreakingChangesSilentlyCorruptRoundTrip(t *testing.T) {
	// Baseline: quantity=uint64#1, owner=string#2.
	base := schema(t, field{"quantity", 1, tUint64}, field{"owner", 2, tString})

	msg := dynamicpb.NewMessage(base)
	msg.Set(base.Fields().ByNumber(1), protoreflect.ValueOfUint64(42))
	msg.Set(base.Fields().ByNumber(2), protoreflect.ValueOfString("alice"))
	oldBytes, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}

	t.Run("field type change (uint64->string) strands the value", func(t *testing.T) {
		// A node running this schema reads owner but LOSES quantity: the varint
		// bytes can't be a string, so #1 decodes empty and 42 becomes unknown.
		newSchema := schema(t, field{"quantity", 1, tString}, field{"owner", 2, tString})
		got := dynamicpb.NewMessage(newSchema)
		if err := proto.Unmarshal(oldBytes, got); err != nil {
			t.Fatalf("unmarshal returned an error; the point is it does NOT: %v", err)
		}
		if q := got.Get(newSchema.Fields().ByNumber(1)).String(); q != "" {
			t.Fatalf("expected quantity to be lost (empty string), got %q", q)
		}
		if len(got.GetUnknown()) == 0 {
			t.Fatal("expected the dropped value to be stranded in unknown fields")
		}
		t.Logf("owner survived (%q) but quantity was silently dropped into unknown fields",
			got.Get(newSchema.Fields().ByNumber(2)).String())
	})

	t.Run("field renumber (owner #2->#5) strands the value", func(t *testing.T) {
		// #2 no longer exists in the new schema, so owner="alice" is lost and #5
		// stays at its zero value.
		newSchema := schema(t, field{"quantity", 1, tUint64}, field{"owner", 5, tString})
		got := dynamicpb.NewMessage(newSchema)
		if err := proto.Unmarshal(oldBytes, got); err != nil {
			t.Fatalf("unmarshal returned an error; the point is it does NOT: %v", err)
		}
		if o := got.Get(newSchema.Fields().ByNumber(5)).String(); o != "" {
			t.Fatalf("expected owner to be lost (empty string), got %q", o)
		}
		if len(got.GetUnknown()) == 0 {
			t.Fatal("expected owner=alice to be stranded in unknown fields")
		}
	})

	t.Run("field rename (owner->owner_name, same #2) preserves the value", func(t *testing.T) {
		// Wire-safe: the binary format keys off field NUMBERS, not names.
		newSchema := schema(t, field{"quantity", 1, tUint64}, field{"owner_name", 2, tString})
		got := dynamicpb.NewMessage(newSchema)
		if err := proto.Unmarshal(oldBytes, got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if o := got.Get(newSchema.Fields().ByNumber(2)).String(); o != "alice" {
			t.Fatalf("expected owner_name to round-trip as \"alice\", got %q", o)
		}
	})
}

// TestBufBreakingGateCatchesWireBreaks proves that `buf breaking` — the check
// wired into CI via `make protos-breaking` — flags each wire-breaking edit that
// TestWireBreakingChangesSilentlyCorruptRoundTrip showed the runtime cannot. It
// uses the same `WIRE` ruleset declared in the repository's root buf.yaml.
func TestBufBreakingGateCatchesWireBreaks(t *testing.T) {
	buf, err := exec.LookPath("buf")
	if err != nil {
		t.Skip("buf not on PATH; install with 'make install-tools' to run this gate check")
	}

	const bufYAML = "version: v2\nmodules:\n  - path: .\n"
	// Keep in sync with the root buf.yaml `breaking.use` ruleset.
	const breakingCfg = `{"version":"v2","breaking":{"use":["WIRE"]}}`
	const baseProto = "syntax = \"proto3\";\npackage demo.v1;\n" +
		"message Token { uint64 quantity = 1; string owner = 2; }\n"

	// Build the baseline image once.
	dir := t.TempDir()
	baseDir := filepath.Join(dir, "base")
	writeModule(t, baseDir, bufYAML, baseProto)
	baseImg := filepath.Join(dir, "base.binpb")
	run(t, buf, "build", baseDir, "-o", baseImg)

	cases := []struct {
		name  string
		token string // the message body that replaces the baseline's
	}{
		{"field type change", "message Token { string quantity = 1; string owner = 2; }"},
		{"field renumber", "message Token { uint64 quantity = 1; string owner = 5; }"},
		{"field delete without reserving its number", "message Token { uint64 quantity = 1; }"},
	}

	for _, tc := range cases {
		t.Run(tc.name+" is caught", func(t *testing.T) {
			newDir := filepath.Join(dir, "new")
			writeModule(t, newDir, bufYAML, "syntax = \"proto3\";\npackage demo.v1;\n"+tc.token+"\n")

			cmd := exec.Command(buf, "breaking", newDir, "--against", baseImg, "--config", breakingCfg)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected buf to report a breaking change, but it passed:\n%s", out)
			}
			if len(out) == 0 {
				t.Fatal("expected buf to explain the breaking change")
			}
			t.Logf("gate caught it:\n%s", out)
		})
	}
}

// writeModule writes a single-file buf module (buf.yaml + token.proto) into dir.
func writeModule(t *testing.T, dir, bufYAML, proto string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "buf.yaml"), []byte(bufYAML), 0o644); err != nil {
		t.Fatalf("write buf.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token.proto"), []byte(proto), 0o644); err != nil {
		t.Fatalf("write token.proto: %v", err)
	}
}

// run executes a command and fails the test on any error.
func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
