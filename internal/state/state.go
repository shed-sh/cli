package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Instance struct {
	ApplicationID string `json:"applicationId"`
	Root          string `json:"root"`
	InstanceID    string `json:"instanceId"`
	ContainerID   string `json:"containerId"`
	ImageID       string `json:"imageId"`
	ImageDigest   string `json:"imageDigest"`
	ContentDigest string `json:"contentDigest"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	URL           string `json:"url"`
}

type Store struct {
	path string
}

func NewStore() (Store, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return Store{}, fmt.Errorf("resolve Shed state directory: %w", err)
	}
	return Store{path: filepath.Join(directory, "shed", "instances.json")}, nil
}

func NewStoreAt(path string) Store { return Store{path: path} }

func (s Store) Load(applicationID string) (Instance, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Instance{}, nil
	}
	if err != nil {
		return Instance{}, fmt.Errorf("read Shed state: %w", err)
	}
	var instances map[string]Instance
	if err := json.Unmarshal(data, &instances); err != nil {
		return Instance{}, fmt.Errorf("decode Shed state: %w", err)
	}
	return instances[applicationID], nil
}

func (s Store) Find(instanceID string) (string, Instance, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", Instance{}, nil
	}
	if err != nil {
		return "", Instance{}, err
	}
	instances := make(map[string]Instance)
	if err := json.Unmarshal(data, &instances); err != nil {
		return "", Instance{}, err
	}
	for applicationID, instance := range instances {
		if instance.InstanceID == instanceID || applicationID == instanceID {
			return applicationID, instance, nil
		}
	}
	return "", Instance{}, nil
}

func (s Store) Save(instance Instance) error {
	if instance.ApplicationID == "" {
		return errors.New("application identity is required")
	}
	data, err := os.ReadFile(s.path)
	instances := make(map[string]Instance)
	if err == nil {
		_ = json.Unmarshal(data, &instances)
	}
	instances[instance.ApplicationID] = instance
	encoded, err := json.MarshalIndent(instances, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Shed state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create Shed state directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".instances-*.tmp")
	if err != nil {
		return fmt.Errorf("create Shed state temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Shed state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Shed state: %w", err)
	}
	if err := os.Rename(temporaryName, s.path); err != nil {
		return fmt.Errorf("commit Shed state: %w", err)
	}
	return nil
}

func (s Store) Remove(applicationID string) error {
	instance, err := s.Load(applicationID)
	if err != nil || instance.ApplicationID == "" {
		return err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	instances := make(map[string]Instance)
	if err := json.Unmarshal(data, &instances); err != nil {
		return err
	}
	delete(instances, applicationID)
	encoded, _ := json.MarshalIndent(instances, "", "  ")
	return os.WriteFile(s.path, append(encoded, '\n'), 0o600)
}

func ApplicationID(root string) string {
	digest := sha256.Sum256([]byte(root))
	return "app_" + hex.EncodeToString(digest[:])[:16]
}
