package syncengine

import "github.com/torfstack/grove/internal/drive"

type Binding struct {
	RemoteRoot string `json:"remote_root"`
	LocalDir   string `json:"local_dir"`
	TokenFile  string `json:"token_file"`
}
type Entry struct {
	Path   string     `json:"path"`
	Remote drive.File `json:"remote"`
}
type Snapshot struct{ Entries []Entry }
type Completed struct {
	Path     string `json:"path"`
	RemoteID string `json:"remote_id"`
	Version  string `json:"remote_version"`
	MD5      string `json:"md5"`
	SHA256   string `json:"sha256"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
}
type ConflictPolicy string

const (
	ConflictAsk       ConflictPolicy = "ask"
	ConflictKeepLocal ConflictPolicy = "keep-local"
	ConflictKeepCloud ConflictPolicy = "keep-cloud"
)

type ConflictSide string

const (
	SideLocal ConflictSide = "local"
	SideCloud ConflictSide = "cloud"
)

type Conflict struct {
	ID            string       `json:"id"`
	Path          string       `json:"path"`
	Baseline      Completed    `json:"baseline"`
	LocalSHA256   string       `json:"local_sha256"`
	LocalSize     int64        `json:"local_size"`
	RemoteID      string       `json:"remote_id"`
	RemoteVersion string       `json:"remote_version"`
	RemoteMD5     string       `json:"remote_md5"`
	RemoteSize    int64        `json:"remote_size"`
	SelectedSide  ConflictSide `json:"selected_side,omitempty"`
}
type FileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}
type Pending struct {
	TempIdentity   *FileIdentity `json:"temp_identity,omitempty"`
	Entry          Entry         `json:"entry"`
	TempPath       string        `json:"temp_path,omitempty"`
	VerifiedSHA256 string        `json:"verified_sha256,omitempty"`
	Phase          string        `json:"phase"`
}
type State struct {
	ConflictPolicy     ConflictPolicy          `json:"conflict_policy,omitempty"`
	Conflicts          []Conflict              `json:"conflicts,omitempty"`
	ProbeIdentities    map[string]FileIdentity `json:"probe_identities,omitempty"`
	ProbePath          string                  `json:"probe_path,omitempty"`
	ProbeEntries       []Entry                 `json:"probe_entries,omitempty"`
	Version            int                     `json:"version"`
	Binding            Binding                 `json:"binding"`
	Completed          []Completed             `json:"completed"`
	Pending            *Pending                `json:"pending,omitempty"`
	Transaction        *Journal                `json:"transaction,omitempty"`
	PopulationComplete bool                    `json:"population_complete"`
}
type LocalEntry struct {
	Path, Kind, SHA256 string
	Size               int64
}
type OperationKind string

const (
	OpMkdir    OperationKind = "mkdir"
	OpDownload OperationKind = "download"
	OpReplace  OperationKind = "replace"
	OpMove     OperationKind = "move"
	OpRecord   OperationKind = "record"
	OpSkip     OperationKind = "skip"
)

type Operation struct {
	Kind   OperationKind
	Entry  Entry
	Before []Completed
}
type Journal struct {
	TempIdentity   *FileIdentity `json:"temp_identity,omitempty"`
	Operation      Operation     `json:"operation"`
	Phase          string        `json:"phase"`
	TempPath       string        `json:"temp_path,omitempty"`
	BackupPath     string        `json:"backup_path,omitempty"`
	VerifiedSHA256 string        `json:"verified_sha256,omitempty"`
	After          []Completed   `json:"after"`
}
type Plan struct{ Operations []Operation }
type Options struct{ ProfileDir, RemoteRoot, LocalDir, TokenFile string }
type Result struct{ Downloaded, CreatedDirectories, Skipped, Updated, Moved int }
