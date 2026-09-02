package definition

import "testing"

func TestWorkloadAndBundleKind(t *testing.T) {
	// The compatibility contract: an Application document is an app shed that
	// ships source. Later document kinds get their own rows here.
	m := Manifest{Kind: ManifestKind}
	if m.WorkloadKind() != "app" {
		t.Fatalf("workload kind = %q, want app", m.WorkloadKind())
	}
	if m.BundleKind() != "source" {
		t.Fatalf("bundle kind = %q, want source", m.BundleKind())
	}
}
