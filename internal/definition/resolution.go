package definition

// Resolution is how a command came by its definition: which file it worked
// from, whether this run wrote that file because none existed, and what
// detection found when it did. Provider is empty when the file was loaded as
// written — an existing definition is authoritative and is never redetected.
type Resolution struct {
	Path     string `json:"path"`
	Created  bool   `json:"created"`
	Provider string `json:"provider,omitempty"`
}
