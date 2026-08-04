package coreserver

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"unicode/utf8"
)

const userPromptMediaType = "text/plain; charset=utf-8"

func validateUserPromptReadRequest(request UserPromptReadRequest) error {
	if !canonicalPublicUUID(request.WorkspaceID) {
		return errors.New("user prompt workspace is invalid")
	}
	if !canonicalPublicUUID(request.Pointer.ObjectID) {
		return errors.New("user prompt object identity is invalid")
	}
	if request.Pointer.Size < 1 || request.Pointer.Size > maxUserRunPromptBytes {
		return errors.New("user prompt object size is outside the prompt bound")
	}
	if request.Pointer.MediaType != userPromptMediaType {
		return errors.New("user prompt object media type is unsupported")
	}
	return nil
}

func validateUserPromptContents(request UserPromptReadRequest, contents []byte) (string, error) {
	if int64(len(contents)) != request.Pointer.Size {
		return "", errors.New("user prompt object size does not match its authority")
	}
	digest := sha256.Sum256(contents)
	if digest != request.Pointer.SHA256 {
		return "", errors.New("user prompt object digest does not match its authority")
	}
	if !utf8.Valid(contents) || len(contents) == 0 || bytes.IndexByte(contents, 0) >= 0 {
		return "", errors.New("user prompt object is not valid bounded UTF-8 text")
	}
	return string(contents), nil
}
