package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		{"valid mp4", "video.mp4", false},
		{"valid mov", "my_video.mov", false},
		{"valid avi", "test.avi", false},
		{"valid mkv", "movie.mkv", false},
		{"valid webm", "clip.webm", false},
		{"empty filename", "", true},
		{"invalid extension", "video.txt", true},
		{"no extension", "video", true},
		{"uppercase extension", "video.MP4", false}, // Should be case-insensitive
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFilename(tt.filename)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateFilename(%q) error = %v, wantErr %v", tt.filename, err, tt.wantErr)
			}
		})
	}
}

func TestValidateFilename_TooLong(t *testing.T) {
	longFilename := make([]byte, MaxFilenameLength+10)
	for i := range longFilename {
		longFilename[i] = 'a'
	}
	longFilename = append(longFilename, '.', 'm', 'p', '4')

	err := validateFilename(string(longFilename))
	if err == nil {
		t.Error("validateFilename() expected error for long filename")
	}
}

func TestValidateContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		wantErr     bool
	}{
		{"valid mp4", "video/mp4", false},
		{"valid quicktime", "video/quicktime", false},
		{"valid avi", "video/x-msvideo", false},
		{"valid matroska", "video/x-matroska", false},
		{"valid webm", "video/webm", false},
		{"empty", "", true},
		{"invalid type", "application/pdf", true},
		{"text type", "text/plain", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateContentType(tt.contentType)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateContentType(%q) error = %v, wantErr %v", tt.contentType, err, tt.wantErr)
			}
		})
	}
}

func TestValidateS3Key(t *testing.T) {
	videoID := "abc-123-def"

	tests := []struct {
		name    string
		key     string
		videoID string
		wantErr bool
	}{
		{"valid key", "uploads/abc-123-def.mp4", videoID, false},
		{"valid key with extension", "uploads/abc-123-def.mov", videoID, false},
		{"wrong prefix", "wrong/abc-123-def.mp4", videoID, true},
		{"path traversal", "uploads/../abc-123-def.mp4", videoID, true},
		{"encoded path traversal", "uploads/%2e%2e/abc-123-def.mp4", videoID, true},
		{"wrong video ID", "uploads/other-id.mp4", videoID, true},
		{"invalid extension", "uploads/abc-123-def.exe", videoID, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateS3Key(tt.key, tt.videoID)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateS3Key(%q, %q) error = %v, wantErr %v", tt.key, tt.videoID, err, tt.wantErr)
			}
		})
	}
}

func TestCORSMiddleware(t *testing.T) {
	allowedOrigins := []string{"https://example.com", "https://test.com"}
	middleware := CORSMiddleware(allowedOrigins)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("allowed origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "https://example.com")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
			t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://example.com")
		}
	})

	t.Run("disallowed origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "https://malicious.com")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Access-Control-Allow-Origin = %q, want empty", got)
		}
	})

	t.Run("preflight request", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "https://example.com")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("Status = %d, want %d", rr.Code, http.StatusNoContent)
		}
	})
}

func TestIsInternalRequest(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       bool
	}{
		{"localhost", "127.0.0.1:8080", true},
		{"10.x network", "10.0.0.1:12345", true},
		{"172.16.x network", "172.16.0.1:12345", true},
		{"192.168.x network", "192.168.1.1:12345", true},
		{"public IP", "203.0.113.1:12345", false},
		{"another public IP", "8.8.8.8:53", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInternalRequest(tt.remoteAddr); got != tt.want {
				t.Errorf("isInternalRequest(%q) = %v, want %v", tt.remoteAddr, got, tt.want)
			}
		})
	}
}

func TestInitUploadHandler_InvalidMethod(t *testing.T) {
	h := &Handlers{}

	req := httptest.NewRequest("GET", "/upload/init", nil)
	rr := httptest.NewRecorder()

	h.InitUploadHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestInitUploadHandler_InvalidJSON(t *testing.T) {
	h := &Handlers{}

	req := httptest.NewRequest("POST", "/upload/init", bytes.NewBufferString("not json"))
	rr := httptest.NewRecorder()

	h.InitUploadHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestInitUploadHandler_InvalidFilename(t *testing.T) {
	h := &Handlers{}

	body := InitUploadRequest{
		Filename:    "video.txt", // Invalid extension
		ContentType: "video/mp4",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/upload/init", bytes.NewBuffer(bodyBytes))
	rr := httptest.NewRecorder()

	h.InitUploadHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestCompleteUploadHandler_InvalidMethod(t *testing.T) {
	h := &Handlers{}

	req := httptest.NewRequest("GET", "/upload/complete", nil)
	rr := httptest.NewRecorder()

	h.CompleteUploadHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestCompleteUploadHandler_MissingVideoID(t *testing.T) {
	h := &Handlers{}

	body := CompleteUploadRequest{
		Key:      "uploads/test.mp4",
		Filename: "test.mp4",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/upload/complete", bytes.NewBuffer(bodyBytes))
	rr := httptest.NewRecorder()

	h.CompleteUploadHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestGetLatestVideoHandler_InvalidMethod(t *testing.T) {
	h := &Handlers{}

	req := httptest.NewRequest("POST", "/latest", nil)
	rr := httptest.NewRecorder()

	h.GetLatestVideoHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}
