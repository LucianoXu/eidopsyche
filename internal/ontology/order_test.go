package ontology

import "testing"

func TestList_SmokeAllSixAuthored(t *testing.T) {
	metas, err := List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mephistopheles", "sebastian", "fire-keeper", "sheherazade", "calcifer", "haku"}
	if len(metas) != len(want) {
		t.Fatalf("got %d prefabs, want %d: %+v", len(metas), len(want), idsOf(metas))
	}
	for i, w := range want {
		if metas[i].ID != w {
			t.Errorf("[%d] got %q, want %q (full order: %v)", i, metas[i].ID, w, idsOf(metas))
		}
	}
}

func idsOf(ms []Meta) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}
