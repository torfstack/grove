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
	Path           string `json:"path"`
	RemoteID       string `json:"remote_id"`
	Version        string `json:"remote_version"`
	ModifiedTime   string `json:"remote_modified_time,omitempty"`
	HeadRevisionID string `json:"head_revision_id,omitempty"`
	MD5            string `json:"md5"`
	SHA256         string `json:"sha256"`
	Kind           string `json:"kind"`
	Size           int64  `json:"size"`
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

type CloudCandidate struct {
	RevisionID    string        `json:"revision_id"`
	ModifiedTime  string        `json:"modified_time,omitempty"`
	MD5           string        `json:"md5"`
	Size          int64         `json:"size"`
	SHA256        string        `json:"sha256"`
	StagePath     string        `json:"stage_path"`
	StageIdentity *FileIdentity `json:"stage_identity"`
}

type Conflict struct {
	CandidateSetVersion   int              `json:"candidate_set_version,omitempty"`
	CloudCandidates       []CloudCandidate `json:"cloud_candidates,omitempty"`
	Deletion              bool             `json:"deletion,omitempty"`
	DeletionSubtree       []Completed      `json:"deletion_subtree,omitempty"`
	DeletionRemoteSubtree []Completed      `json:"deletion_remote_subtree,omitempty"`
	ID                    string           `json:"id"`
	Path                  string           `json:"path"`
	Baseline              Completed        `json:"baseline"`
	LocalSHA256           string           `json:"local_sha256"`
	LocalSize             int64            `json:"local_size"`
	RemoteID              string           `json:"remote_id"`
	RemoteVersion         string           `json:"remote_version"`
	RemoteModifiedTime    string           `json:"remote_modified_time,omitempty"`
	RemoteMD5             string           `json:"remote_md5"`
	RemoteSize            int64            `json:"remote_size"`
	RemoteRevisionID      string           `json:"remote_revision_id,omitempty"`
	CloudRevisionID       string           `json:"cloud_revision_id,omitempty"`
	CloudMD5              string           `json:"cloud_md5,omitempty"`
	CloudSize             int64            `json:"cloud_size,omitempty"`
	LocalStagePath        string           `json:"local_stage_path,omitempty"`
	LocalStageIdentity    *FileIdentity    `json:"local_stage_identity,omitempty"`
	CloudStagePath        string           `json:"cloud_stage_path,omitempty"`
	CloudStageIdentity    *FileIdentity    `json:"cloud_stage_identity,omitempty"`
	CloudSHA256           string           `json:"cloud_sha256,omitempty"`
	SelectedSide          ConflictSide     `json:"selected_side,omitempty"`
	SelectedCandidate     int              `json:"selected_candidate,omitempty"`
}

type ConflictCandidateView struct {
	Ordinal      int          `json:"ordinal"`
	Side         ConflictSide `json:"side"`
	Description  string       `json:"description,omitempty"`
	ModifiedTime string       `json:"modified_time,omitempty"`
	Size         int64        `json:"size"`
	Selected     bool         `json:"selected"`
}

type ConflictDetail struct {
	ID         string                  `json:"id"`
	Path       string                  `json:"path"`
	Candidates []ConflictCandidateView `json:"candidates"`
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
	OpUpload   OperationKind = "upload"
	OpCreate   OperationKind = "create"
	OpConflict OperationKind = "conflict"
	OpTrash    OperationKind = "trash"
)

type Operation struct {
	Kind            OperationKind
	Entry           Entry
	Local           LocalEntry
	OperationMarker string
	Conflict        *Conflict
	ResolutionSide  ConflictSide
	Before          []Completed
	RemoteSubtree   []Completed
}
type Journal struct {
	CandidateCaptures         []CandidateCapture `json:"candidate_captures,omitempty"`
	TempIdentity              *FileIdentity      `json:"temp_identity,omitempty"`
	Operation                 Operation          `json:"operation"`
	Phase                     string             `json:"phase"`
	TempPath                  string             `json:"temp_path,omitempty"`
	BackupPath                string             `json:"backup_path,omitempty"`
	VerifiedSHA256            string             `json:"verified_sha256,omitempty"`
	StagePath                 string             `json:"stage_path,omitempty"`
	StageIdentity             *FileIdentity      `json:"stage_identity,omitempty"`
	StageSHA256               string             `json:"stage_sha256,omitempty"`
	StageMD5                  string             `json:"stage_md5,omitempty"`
	StageSize                 int64              `json:"stage_size,omitempty"`
	AllocatedID               string             `json:"allocated_id,omitempty"`
	OperationMarker           string             `json:"operation_marker,omitempty"`
	PreflightRevisionID       string             `json:"preflight_revision_id,omitempty"`
	PreflightPinWasKept       *bool              `json:"preflight_pin_was_kept,omitempty"`
	PinnedRevisionID          string             `json:"pinned_revision_id,omitempty"`
	CompetingRevisionIDs      []string           `json:"competing_revision_ids,omitempty"`
	RevisionHistoryUnverified bool               `json:"revision_history_unverified,omitempty"`
	CloudStagePath            string             `json:"cloud_stage_path,omitempty"`
	CloudStageIdentity        *FileIdentity      `json:"cloud_stage_identity,omitempty"`
	CloudSHA256               string             `json:"cloud_sha256,omitempty"`
	CloudRevisionID           string             `json:"cloud_revision_id,omitempty"`
	CloudMD5                  string             `json:"cloud_md5,omitempty"`
	CloudSize                 int64              `json:"cloud_size,omitempty"`
	After                     []Completed        `json:"after"`
}

type CandidateCapture struct {
	Candidate CloudCandidate `json:"candidate"`
	Phase     string         `json:"phase"`
}
type Plan struct{ Operations []Operation }
type Options struct {
	ProfileDir, RemoteRoot, LocalDir, TokenFile string
	TwoWay                                      bool
	deletionResolutionID                        string
}
type Result struct{ Downloaded, Uploaded, CreatedDirectories, Skipped, Updated, Moved, Trashed int }
