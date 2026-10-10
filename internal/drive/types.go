package drive

import (
	"context"
	"io"
)

type File struct {
	ID, Name, MIMEType, MD5, Version, DriveID string
	Parents                                   []string
	Size                                      int64
	HasSize, OwnedByMe, CanDownload, Trashed  bool
	Properties                                map[string]string
}
type Page struct {
	Files      []File
	NextToken  string
	Incomplete bool
}
type Create struct {
	Name, MIMEType, ParentID string
	Properties               map[string]string
}
type API interface {
	Get(context.Context, string) (File, error)
	List(context.Context, string, string) (Page, error)
	Download(context.Context, string, io.Writer) error
	Create(context.Context, Create, io.Reader) (File, error)
	Trash(context.Context, string) error
}

const FolderMIME = "application/vnd.google-apps.folder"

type wireFile struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	MIMEType     string            `json:"mimeType"`
	MD5          string            `json:"md5Checksum"`
	Version      string            `json:"version"`
	DriveID      string            `json:"driveId"`
	Parents      []string          `json:"parents"`
	Size         *int64            `json:"size,string"`
	OwnedByMe    bool              `json:"ownedByMe"`
	Trashed      bool              `json:"trashed"`
	Properties   map[string]string `json:"appProperties"`
	Capabilities struct {
		CanDownload bool `json:"canDownload"`
	} `json:"capabilities"`
}

func (w wireFile) file() File {
	f := File{ID: w.ID, Name: w.Name, MIMEType: w.MIMEType, MD5: w.MD5, Version: w.Version, DriveID: w.DriveID, Parents: w.Parents, OwnedByMe: w.OwnedByMe, CanDownload: w.Capabilities.CanDownload, Trashed: w.Trashed, Properties: w.Properties}
	if w.Size != nil {
		f.HasSize = true
		f.Size = *w.Size
	}
	return f
}
