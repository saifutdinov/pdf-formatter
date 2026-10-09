package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Builtin fonts: ids do not depend on the free/ or licensed/ subfolder (old
// documents keep their fonts), and commercial ones are marked as such.
func TestBuiltinFontSources(t *testing.T) {
	lib := NewLibrary(t.TempDir(), []string{"../../fonts"}, nil)
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, _, ready := lib.List(); ready || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fonts, _, _ := lib.List()
	sources := map[string]int{}
	for _, f := range fonts {
		sources[f.Source]++
		if f.PSName == "PTSans-Regular" && f.ID != fontID(filepath.Join("../../fonts", "PTSans.ttc"), f.PSName) {
			t.Errorf("PT Sans id %s depends on its folder", f.ID)
		}
	}
	if sources["builtin"] == 0 {
		t.Errorf("no free fonts: %v", sources)
	}
	if _, err := os.Stat("../../fonts/licensed"); err == nil && sources["licensed"] == 0 {
		t.Errorf("licensed fonts not marked: %v", sources)
	}
}
