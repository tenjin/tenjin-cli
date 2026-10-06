package guides

import (
	"strings"
	"testing"
)

func TestEveryGuideIsEmbedded(t *testing.T) {
	for _, name := range Names() {
		g, ok := For(name)
		if !ok || len(g.Content) < 10000 || !strings.Contains(g.Content, "Tenjin") {
			t.Errorf("%s: ok = %v, %d bytes", name, ok, len(g.Content))
		}
	}
	if g, ok := For("Ionic"); !ok || g.Name != "capacitor" {
		t.Errorf("ionic = %+v", g)
	}
	if _, ok := For("unreal"); ok {
		t.Error("there is no Unreal guide")
	}
	if len(Revision()) < 7 || !strings.Contains(License(), "MIT") {
		t.Errorf("revision = %q", Revision())
	}
}
