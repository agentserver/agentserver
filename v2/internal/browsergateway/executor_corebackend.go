package browsergateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maxCoreExecutorResponseBytes = int64(128 * 1024)

func (backend *CoreRunBackend) CreateExecutorResource(
	ctx context.Context,
	bearer, workspaceID string,
	input corecontract.CreateExecutorResourceRequest,
) (corecontract.CreateExecutorResourceResponse, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return corecontract.CreateExecutorResourceResponse{}, fmt.Errorf("encode core executor creation request: %w", err)
	}
	request, err := backend.executorRequest(ctx, http.MethodPost, corecontract.CreateExecutorResourcePath(workspaceID), bearer, bytes.NewReader(raw))
	if err != nil {
		return corecontract.CreateExecutorResourceResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, body, err := backend.doBounded(request, maxCoreExecutorResponseBytes)
	if err != nil {
		return corecontract.CreateExecutorResourceResponse{}, err
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		return corecontract.CreateExecutorResourceResponse{}, errors.New("core executor creation response is missing Cache-Control no-store")
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return corecontract.CreateExecutorResourceResponse{}, decodePublicCoreError(response.StatusCode, body)
	}
	var result corecontract.CreateExecutorResourceResponse
	if err := decodeStrictCoreJSON(body, &result); err != nil {
		return corecontract.CreateExecutorResourceResponse{}, fmt.Errorf("decode core executor creation response: %w", err)
	}
	if (response.StatusCode == http.StatusCreated) != result.Created {
		return corecontract.CreateExecutorResourceResponse{}, errors.New("core executor creation status and created flag disagree")
	}
	return result, nil
}

func (backend *CoreRunBackend) IssueExecutorEnrollmentToken(
	ctx context.Context,
	bearer, workspaceID, executorID, idempotencyKey string,
) (corecontract.IssueExecutorEnrollmentTokenResponse, error) {
	request, err := backend.executorRequest(
		ctx, http.MethodPost,
		corecontract.IssueExecutorEnrollmentTokenPath(workspaceID, executorID), bearer, nil,
	)
	if err != nil {
		return corecontract.IssueExecutorEnrollmentTokenResponse{}, err
	}
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response, body, err := backend.doBounded(request, maxCoreExecutorResponseBytes)
	if err != nil {
		return corecontract.IssueExecutorEnrollmentTokenResponse{}, err
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		return corecontract.IssueExecutorEnrollmentTokenResponse{}, errors.New("core enrollment token response is missing Cache-Control no-store")
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return corecontract.IssueExecutorEnrollmentTokenResponse{}, decodePublicCoreError(response.StatusCode, body)
	}
	var result corecontract.IssueExecutorEnrollmentTokenResponse
	if err := decodeStrictCoreJSON(body, &result); err != nil {
		return corecontract.IssueExecutorEnrollmentTokenResponse{}, fmt.Errorf("decode core enrollment token response: %w", err)
	}
	if (response.StatusCode == http.StatusCreated) != result.Created {
		return corecontract.IssueExecutorEnrollmentTokenResponse{}, errors.New("core enrollment token status and created flag disagree")
	}
	return result, nil
}

func (backend *CoreRunBackend) executorRequest(
	ctx context.Context,
	method, path, bearer string,
	body *bytes.Reader,
) (*http.Request, error) {
	if backend == nil || backend.baseURL == nil || backend.httpClient == nil {
		return nil, errors.New("core executor backend is unavailable")
	}
	endpoint := backend.endpoint(path)
	var requestBody io.Reader
	if body != nil {
		requestBody = body
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("construct core executor request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Accept", "application/json")
	return request, nil
}

var _ ExecutorResourceBackend = (*CoreRunBackend)(nil)
