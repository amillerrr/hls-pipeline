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
)

type VideoMetadata struct {
	FileID    string `json:"file_id"`
	StreamURL string `json:"stream_url"`
	Timestamp string `json:"timestamp"`
}

func (h *APIHandler) GetLatestVideoHandler(w http.ResponseWriter, r *http.Request) {
	bucket := os.Getenv("PROCESSED_BUCKET")
	cdnDomain := os.Getenv("CDN_DOMAIN")
    
	if bucket == "" || cdnDomain == "" {
		http.Error(w, "Configuration Error: Missing Bucket/CDN Env", http.StatusInternalServerError)
		return
	}

	// List objects 
	output, err := h.S3Client.ListObjectsV2(r.Context(), &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		h.Logger.Error("Failed to list processed bucket", "error", err)
		http.Error(w, "Failed to fetch videos", http.StatusInternalServerError)
		return
	}

	// Filter for video asset
	var candidates []VideoMetadata
	for _, obj := range output.Contents {
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

	// Return video or empty JSON
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*") 

	if len(candidates) > 0 {
		json.NewEncoder(w).Encode(candidates[0])
	} else {
		w.Write([]byte("{}"))
	}
}
