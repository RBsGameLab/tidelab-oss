// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"math"
	"testing"
)

// R-147: what the parser shares, and that sharing changes no value.

func TestParserSharesRepeatedTextsNumbersAndKeyLists(t *testing.T) {
	v, err := ParseJSON([]byte(`[{"a":1,"b":"x"},{"a":1,"b":"x"},{"b":"x","a":2}]`))
	if err != nil {
		t.Fatal(err)
	}
	items := v.(*Array).Items
	o0, o1, o2 := items[0].(*Object), items[1].(*Object), items[2].(*Object)
	if &o0.Keys[0] != &o1.Keys[0] {
		t.Error("objects with the same keys in the same order do not share their key list")
	}
	if &o0.Keys[0] == &o2.Keys[0] {
		t.Error("objects with keys in another order share a key list")
	}
	if o0.Values[0] != o1.Values[0] || o0.Values[1] != o1.Values[1] {
		t.Error("equal values are not equal")
	}
	if o0.Values[0] == o2.Values[1] {
		t.Error("1 and 2 compare equal")
	}
	if o2.Keys[0] != "b" || o2.Values[1] != Number(2) {
		t.Errorf("third object read as %v %v", o2.Keys, o2.Values)
	}
}

func TestParserKeepsMinusZeroAndLongTexts(t *testing.T) {
	long := ""
	for len(long) <= maxShared {
		long += "abcdefgh"
	}
	v, err := ParseJSON([]byte(`[0, -0, "` + long + `", "` + long + `"]`))
	if err != nil {
		t.Fatal(err)
	}
	items := v.(*Array).Items
	if math.Signbit(float64(items[0].(Number))) || !math.Signbit(float64(items[1].(Number))) {
		t.Errorf("0 and -0 read as %v and %v", items[0], items[1])
	}
	if items[2].(String) != String(long) || items[3].(String) != String(long) {
		t.Error("long texts changed")
	}
}

func TestParserNestingAndDuplicateKeysStillRight(t *testing.T) {
	v, err := ParseJSON([]byte(`{"k":[[1,2],[],{"z":[3]}],"k":9,"e":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := JSONText(v); got != `{"k":[[1,2],[],{"z":[3]}],"e":{}}` {
		t.Errorf("read as %s", got)
	}
}

func TestArrayAndMapCodecsAreMadeOnce(t *testing.T) {
	if ArrayOf(IntC) != ArrayOf(IntC) {
		t.Error("ArrayOf(IntC) made a second codec")
	}
	if MapOf(StringC, IntC) != MapOf(StringC, IntC) {
		t.Error("MapOf(StringC, IntC) made a second codec")
	}
	if any(ArrayOf(IntC)) == any(ArrayOf(StringC)) {
		t.Error("two element codecs share an array codec")
	}
}
