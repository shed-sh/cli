package state

import (
	"path/filepath"
	"testing"
)

func TestApplicationIDIsStableForCanonicalRoot(t *testing.T) {
	a := ApplicationID(filepath.Join("/tmp", "project"))
	b := ApplicationID(filepath.Join("/tmp", ".", "project"))
	if a != b || len(a) != len("app_")+16 {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

func TestStoreRoundTripAndRemove(t *testing.T) {
	store := NewStoreAt(filepath.Join(t.TempDir(), "instances.json"))
	value := Instance{ApplicationID: "app_123", Root: "/tmp/project", InstanceID: "inst_123", HostPort: 4000}
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(value.ApplicationID)
	if err != nil || got.InstanceID != value.InstanceID {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	_, got, err = store.Find(value.InstanceID)
	if err != nil || got.ApplicationID != value.ApplicationID {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if err := store.Remove(value.ApplicationID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(value.ApplicationID); err != nil || got.InstanceID != "" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
