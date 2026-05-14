package forgectl

import "testing"

func TestVersionsFromLabels(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   ImageVersions
	}{
		{
			name: "both present",
			labels: map[string]string{
				"org.eidopsyche.eidos-version":       "v0.11.3",
				"org.eidopsyche.claude-code-version": "2.1.140",
			},
			want: ImageVersions{Eidos: "v0.11.3", ClaudeCode: "2.1.140"},
		},
		{
			name:   "neither present",
			labels: map[string]string{"other.label": "x"},
			want:   ImageVersions{},
		},
		{
			name:   "only eidos",
			labels: map[string]string{"org.eidopsyche.eidos-version": "v0.11.3"},
			want:   ImageVersions{Eidos: "v0.11.3"},
		},
		{
			name:   "nil map",
			labels: nil,
			want:   ImageVersions{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := VersionsFromLabels(tc.labels)
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
