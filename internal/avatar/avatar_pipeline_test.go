package avatar

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type fakeProcessor struct {
	calls []string
}

func (f *fakeProcessor) Upload(_ context.Context, _ string, _ []byte, _ string) (imageData, error) {
	f.calls = append(f.calls, "upload")
	return imageData{ID: "raw-1"}, nil
}

func (f *fakeProcessor) SmartCrop(_ context.Context, image, _ string) (imageData, error) {
	f.calls = append(f.calls, "crop:"+image)
	return imageData{ID: "crop-1"}, nil
}

func (f *fakeProcessor) Optimize(_ context.Context, image, _ string) (imageData, error) {
	f.calls = append(f.calls, "optimize:"+image)
	return imageData{ID: "avatar-1", URL: "https://cdn.example/avatar-1.webp"}, nil
}

func (f *fakeProcessor) UpdateUserAvatar(_ context.Context, userID string, _ imageData, _ string) error {
	f.calls = append(f.calls, "user:"+userID)
	return nil
}

func TestAvatarWorkflowDecision(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		content    []byte
		wantStatus int
		wantState  string
		wantCalls  []string
	}{
		{
			name: "valid PNG enters moderation queue", filename: "mage.png", content: []byte("image"),
			wantStatus: http.StatusAccepted, wantState: "pending_review",
			wantCalls: []string{"upload", "crop:raw-1", "optimize:crop-1", "user:player-42"},
		},
		{name: "unsupported file stops before pipeline", filename: "notes.txt", content: []byte("text"), wantStatus: http.StatusBadRequest},
		{name: "empty image stops before pipeline", filename: "empty.webp", content: nil, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := &fakeProcessor{}
			handler := NewHandler(processor, 1024)
			req := multipartRequest(t, tt.filename, tt.content)
			res := httptest.NewRecorder()

			handler.ServeHTTP(res, req)

			if res.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", res.Code, tt.wantStatus, res.Body.String())
			}
			if !reflect.DeepEqual(processor.calls, tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", processor.calls, tt.wantCalls)
			}
			if tt.wantState != "" {
				var result uploadResult
				if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.ModerationState != tt.wantState || result.Event != "avatar.processed" {
					t.Fatalf("result = %+v", result)
				}
			}
		})
	}
}

func TestInfraiClientRequestContracts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		switch r.URL.Path {
		case "/v1/image/upload":
			want := map[string]any{"file": "aW1hZ2U=", "filename": "avatar.png"}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("upload body = %#v, want %#v", body, want)
			}
			w.Write([]byte(`{"ok":true,"data":{"id":"raw-1"}}`))
		case "/v1/auth/user/update/player-42":
			if body["user_id"] != "player-42" {
				t.Errorf("update user_id = %#v, want player-42", body["user_id"])
			}
			w.Write([]byte(`{"ok":true,"data":{}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewInfraiClient(server.URL, "test-key", server.Client())
	if _, err := client.Upload(context.Background(), "avatar.png", []byte("image"), "request-1"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if err := client.UpdateUserAvatar(context.Background(), "player-42", imageData{ID: "avatar-1"}, "request-1"); err != nil {
		t.Fatalf("UpdateUserAvatar: %v", err)
	}
}

func multipartRequest(t *testing.T, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("user_id", "player-42"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/avatars", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Idempotency-Key", "upload-42")
	return req
}
