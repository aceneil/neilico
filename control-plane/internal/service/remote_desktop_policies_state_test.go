package service

import "testing"

func TestFeatureMapDefaultsAndThreeStates(t *testing.T) {
	for _, feature := range []string{FeatureMesh, FeatureRemoteDesktop} {
		for _, state := range []FeatureState{FeatureAllow, FeatureDeny, FeatureForced} {
			t.Run(feature+"/"+string(state), func(t *testing.T) {
				values := featureMap(map[string]any{feature: string(state)}, false)
				if values[feature] != string(state) {
					t.Fatalf("%s = %q, want %q", feature, values[feature], state)
				}
				if !state.Valid() {
					t.Fatalf("state %q should be valid", state)
				}
			})
		}
	}

	defaults := featureMap(nil, false)
	if defaults[FeatureMesh] != string(FeatureAllow) || defaults[FeatureRemoteDesktop] != string(FeatureDeny) {
		t.Fatalf("unsafe defaults: %#v", defaults)
	}
	legacy := featureMap(nil, true)
	if legacy[FeatureRemoteDesktop] != string(FeatureAllow) {
		t.Fatalf("legacy true was not mapped to allow: %#v", legacy)
	}
}

func TestUnknownFeatureDefaultsDeny(t *testing.T) {
	values := featureMap(map[string]any{"file_transfer": "bogus"}, false)
	if values["file_transfer"] != string(FeatureDeny) {
		t.Fatalf("unknown invalid feature = %q, want deny", values["file_transfer"])
	}
}
