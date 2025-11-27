package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/amillerrr/hls-pipeline/internal/logger"
)

type VideoMetadata struct {
	FileID    string `json:"file_id"`
	StreamURL string `json:"stream_url"`
	Timestamp string `json:"timestamp"`
}

func (h *APIHandler) GetLatestVideoHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Max-Age", "86400")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	bucket := os.Getenv("PROCESSED_BUCKET")
	cdnDomain := os.Getenv("CDN_DOMAIN")
    
	if bucket == "" || cdnDomain == "" {
		logger.Error(ctx, h.Logger, "Missing configuration", "bucket", bucket, "cdn", cdnDomain)
		http.Error(w, "Server configuration error", http.StatusInternalServerError)
		return
	}

	// List objects processed 
	output, err := h.S3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		logger.Error(ctx, h.Logger, "Failed to list processed bucket", "error", err)
		http.Error(w, "Failed to fetch videos", http.StatusInternalServerError)
		return
	}

	// Filter for master playlist
	var candidates []VideoMetadata
	for _, obj := range output.Contents {
		if obj.Key == nil || obj.LastModified == nil {
			continue
		}
		key := *obj.Key
		if strings.HasSuffix(key, "master.m3u8") {
			parts := strings.Split(key, "/")
			if len(parts) > 1 {
				id := parts[0]
				url := fmt.Sprintf("https://%s/%s", cdnDomain, key)
				
				candidates = append(candidates, VideoMetadata{
					FileID:    id,
					StreamURL: url,
					Timestamp: obj.LastModified.String(),
				})
			}
		}
	}

	// Sort by Timestamp
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Timestamp > candidates[j].Timestamp
	})

	// Return video or empty response
	if len(candidates) > 0 {
		if err := json.NewEncoder(w).Encode(candidates[0]); err != nil {
			logger.Error(ctx, h.Logger, "Failed to encode response", "error", err)
		}
	} else {
		w.Write([]byte("{}"))
	}
}

// Return a list of processed videos 
func (h *APIHandler) ListVideosHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	bucket := os.Getenv("PROCESSED_BUCKET")
	cdnDomain := os.Getenv("CDN_DOMAIN")

	if bucket == "" || cdnDomain == "" {
		http.Error(w, "Server configuration error", http.StatusInternalServerError)
		return
	}

	output, err := h.S3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		logger.Error(ctx, h.Logger, "Failed to list bucket", "error", err)
		http.Error(w, "Failed to fetch videos", http.StatusInternalServerError)
		return
	}

	var videos []VideoMetadata
	for _, obj := range output.Contents {
		if obj.Key == nil || obj.LastModified == nil {
			continue
		}
		key := *obj.Key
		if strings.HasSuffix(key, "master.m3u8") {
			parts := strings.Split(key, "/")
			if len(parts) > 1 {
				videos = append(videos, VideoMetadata{
					FileID:    parts[0],
					StreamURL: fmt.Sprintf("https://%s/%s", cdnDomain, key),
					Timestamp: obj.LastModified.Format("2006-01-02T15:04:05Z07:00"),
				})
			}
		}
	}

	// Sort by timestamp
	sort.Slice(videos, func(i, j int) bool {
		return videos[i].Timestamp > videos[j].Timestamp
	})

	json.NewEncoder(w).Encode(map[string]any{
		"videos": videos,
		"count":  len(videos),
	})
}
