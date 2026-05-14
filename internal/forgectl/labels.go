package forgectl

import "context"

// Label keys baked into the mind-form image by docker/mindform/Dockerfile.
// Stable contract between the image and the host binary; never rename
// without a coupled image + host release.
const (
	LabelEidosVersion      = "org.eidopsyche.eidos-version"
	LabelClaudeCodeVersion = "org.eidopsyche.claude-code-version"
)

// ImageVersions is the parsed view of the two org.eidopsyche.* labels
// declared on a mind-form image. Missing labels surface as empty
// strings; callers render those as "unknown".
type ImageVersions struct {
	Eidos      string
	ClaudeCode string
}

// VersionsFromLabels extracts the eidos and claude-code versions from
// a labels map (typically Config.Labels from docker image inspect).
// Tolerates nil / missing keys.
func VersionsFromLabels(labels map[string]string) ImageVersions {
	return ImageVersions{
		Eidos:      labels[LabelEidosVersion],
		ClaudeCode: labels[LabelClaudeCodeVersion],
	}
}

// ImageInspectLabels returns the Config.Labels map of the image at ref.
// Used by forge upgrade preflight and forge status to read embedded
// version metadata without starting a container. Returns an empty map
// (not nil) when the image carries no labels.
func (r *realClient) ImageInspectLabels(ctx context.Context, ref string) (map[string]string, error) {
	resp, err := r.c.ImageInspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	if resp.Config == nil || resp.Config.Labels == nil {
		return map[string]string{}, nil
	}
	return resp.Config.Labels, nil
}
