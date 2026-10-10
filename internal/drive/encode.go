package drive

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/textproto"
)

func encodeFixture(metadata map[string]any, content io.Reader) ([]byte, string, error) {
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, "", errors.New("cannot encode fixture metadata")
	}
	ct := "application/json"
	if content != nil {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
		if err != nil {
			return nil, "", err
		}
		if _, err = part.Write(data); err != nil {
			return nil, "", err
		}
		part, err = writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/octet-stream"}})
		if err != nil {
			return nil, "", err
		}
		if _, err = io.Copy(part, content); err != nil {
			return nil, "", errors.New("cannot read fixture payload")
		}
		if err = writer.Close(); err != nil {
			return nil, "", err
		}
		data = body.Bytes()
		ct = "multipart/related; boundary=" + writer.Boundary()
	}
	return data, ct, nil
}
