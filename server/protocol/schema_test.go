// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package protocol

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// The schema the client's Aki.Protocol has (written by tools/Tidelab.ProtoGen).
var schemaPath = "schema.tsv"

// pascal is the C# property name protoc gives a proto field name (Google.Protobuf's rule:
// the first letter and every letter after '_' or a digit upper-cased, '_' dropped).
func pascal(name string) string {
	var b strings.Builder
	up := true
	for _, r := range name {
		switch {
		case r == '_':
			up = true
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			up = true
		default:
			if up && r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
			up = false
		}
	}
	return b.String()
}

// qualified is the name schema.tsv gives a type: its C# name below Tidelab.Protocol, i.e. the
// file's csharp_namespace after "Tidelab.Protocol", then the nesting path ("Ai.Int2Bool",
// "PbMapMarkType.ENUMS").
func qualified(d protoreflect.Descriptor) string {
	fd := d.ParentFile()
	rel := strings.TrimPrefix(string(d.FullName()), string(fd.Package())+".")
	ns := strings.TrimPrefix(strings.TrimPrefix(fd.Options().(*descriptorpb.FileOptions).GetCsharpNamespace(), "Tidelab.Protocol"), ".")
	if ns == "" {
		return rel
	}
	return ns + "." + rel
}

func typeName(f protoreflect.FieldDescriptor) string {
	switch f.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return qualified(f.Message())
	case protoreflect.EnumKind:
		return qualified(f.Enum())
	default:
		return f.Kind().String()
	}
}

// ourLines rebuilds schema.tsv's lines from the compiled package's descriptors.
func ourLines(t *testing.T) []string {
	ids := map[string]MessageID{}
	for id, n := range messageNames {
		ids[n] = id
	}
	var lines []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !isOurs(fd.Path()) {
			return true
		}
		var enums func(es protoreflect.EnumDescriptors)
		enums = func(es protoreflect.EnumDescriptors) {
			for i := 0; i < es.Len(); i++ {
				e := es.Get(i)
				q := qualified(e)
				lines = append(lines, "enum	"+q)
				for j := 0; j < e.Values().Len(); j++ {
					v := e.Values().Get(j)
					n := int64(v.Number())
					num := fmt.Sprintf("%011d", n)
					if n < 0 {
						num = fmt.Sprintf("-%011d", -n)
					}
					lines = append(lines, fmt.Sprintf("value	%s	%s	%s", q, num, strings.TrimPrefix(string(v.Name()), string(e.Name())+"_")))
				}
			}
		}
		var messages func(ms protoreflect.MessageDescriptors)
		messages = func(ms protoreflect.MessageDescriptors) {
			for i := 0; i < ms.Len(); i++ {
				m := ms.Get(i)
				if m.IsMapEntry() {
					continue
				}
				q := qualified(m)
				if id, ok := ids[string(m.Name())]; ok && m.Parent() == m.ParentFile() {
					lines = append(lines, fmt.Sprintf("message	%s	%d", q, id), fmt.Sprintf("id	%s	%d", m.Name(), id))
				} else {
					lines = append(lines, "message	"+q+"	-")
				}
				for j := 0; j < m.Fields().Len(); j++ {
					f := m.Fields().Get(j)
					prop := pascal(string(f.Name()))
					if prop == "Types" || prop == "Descriptor" || prop == string(m.Name()) {
						prop += "_" // C# protoc: these would clash with members of the generated class
					}
					head := fmt.Sprintf("field	%s	%05d	%s", q, f.Number(), prop)
					var line string
					switch {
					case f.IsMap():
						line = fmt.Sprintf("%s	map	%s	key=%s", head, typeName(f.MapValue()), typeName(f.MapKey()))
					case f.IsList():
						line = head + "	repeated	" + typeName(f)
					case f.HasOptionalKeyword():
						line = head + "	optional	" + typeName(f)
					default:
						line = head + "	singular	" + typeName(f)
					}
					if o := f.ContainingOneof(); o != nil && !o.IsSynthetic() {
						line += "	oneof=" + pascal(string(o.Name()))
					}
					lines = append(lines, line)
				}
				enums(m.Enums())
				messages(m.Messages())
			}
		}
		enums(fd.Enums())
		messages(fd.Messages())
		return true
	})
	if len(lines) == 0 {
		t.Fatal("no aki_protocol files registered")
	}
	return lines
}

func TestOurPackageHasTheClientsSchema(t *testing.T) {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var client []string
	for _, l := range strings.Split(string(bytes.ReplaceAll(raw, []byte("\r"), nil)), "\n") {
		if l != "" {
			client = append(client, l)
		}
	}
	ours := ourLines(t)
	diff := func(a, b []string) []string {
		in := map[string]bool{}
		for _, l := range b {
			in[l] = true
		}
		var out []string
		for _, l := range a {
			if !in[l] {
				out = append(out, l)
			}
		}
		sort.Strings(out)
		return out
	}
	missing, extra := diff(client, ours), diff(ours, client)
	if len(missing) > 0 || len(extra) > 0 {
		n := func(s []string) []string { return s[:min(len(s), 10)] }
		t.Fatalf("client only (%d):\n%s\nours only (%d):\n%s", len(missing), strings.Join(n(missing), "\n"), len(extra), strings.Join(n(extra), "\n"))
	}
	if len(client) != len(ours) {
		t.Fatalf("%d lines in schema.tsv, %d from our package", len(client), len(ours))
	}
}

func TestEveryMessageIDHasItsMessage(t *testing.T) {
	for id, name := range messageNames {
		m := New(id)
		if m == nil {
			t.Fatalf("no message for %s (%d)", name, id)
		}
		if got := string(m.ProtoReflect().Descriptor().Name()); got != name {
			t.Fatalf("id %d is %s, want %s", id, got, name)
		}
		if back, ok := IDOf(m); !ok || back != id {
			t.Fatalf("IDOf(%s) = %d, %v", name, back, ok)
		}
	}
	if New(0) != nil {
		t.Fatal("id 0 has a message")
	}
}

func TestIDsAreTheClients(t *testing.T) {
	// EMessageId in reference/client-src-3.8/Aki.Protocol; specs/network.md §5.1.
	for id, want := range map[MessageID]MessageID{IDProtoKeyRequest: 111, IDProtoKeyResponse: 112, IDLoginRequest: 103, IDLoginResponse: 104, IDAceAntiDataPush: 21559} {
		if id != want {
			t.Fatalf("%s = %d, want %d", id.Name(), id, want)
		}
	}
}

func marshal(t *testing.T, m proto.Message) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProtoKeyResponseEncodesAsTheClientReadsIt(t *testing.T) {
	// Type = 2 (field 2, varint), Key (field 3, bytes); Code left 0.
	m := &ProtoKeyResponse{Type: 2, Key: []byte{1, 2}}
	if got := marshal(t, m); !bytes.Equal(got, []byte{0x10, 0x02, 0x1A, 0x02, 0x01, 0x02}) {
		t.Fatalf("% x", got)
	}
	back := New(IDProtoKeyResponse)
	if err := proto.Unmarshal(marshal(t, m), back); err != nil || !proto.Equal(back, m) {
		t.Fatalf("round trip: %v %v", back, err)
	}
}

func TestSInt32FieldsAreZigZag(t *testing.T) {
	// BoardGridDynamicConfig.RowIndex: the client reads it with ReadSInt32.
	if got := marshal(t, &BoardGridDynamicConfig{RowIndex: -1}); !bytes.Equal(got, []byte{0x08, 0x01}) {
		t.Fatalf("% x", got)
	}
}

func TestOptionalFieldSendsZeroWhenSet(t *testing.T) {
	// DamageContext.SourceType is proto3 optional: set to its zero value it is still written.
	zero := DamageSourceType_DamageSourceType_FromBullet
	if got := marshal(t, &DamageContext{SourceType: &zero}); !bytes.Equal(got, []byte{0x08, 0x00}) {
		t.Fatalf("% x", got)
	}
	if got := marshal(t, &DamageContext{}); len(got) != 0 {
		t.Fatalf("% x", got)
	}
}
