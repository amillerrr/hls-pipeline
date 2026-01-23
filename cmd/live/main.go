package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/go-chi/chi/v5"
	
	"github.com/amillerrr/hls-pipeline/internal/live"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := loadConfig()

	// AWS SDK
	awsCfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(cfg.AWSRegion))
	if err != nil {
		logger.Error("failed to load aws config", "error", err)
		os.Exit(1)
	}
	
	dynamoClient := dynamodb.NewFromConfig(awsCfg)

	// Initialize Store
	store := live.NewDynamoDBStore(dynamoClient, cfg.DynamoDBTable)

	// Initialize Manager
	managerCfg := &live.ManagerConfig{
		OutputBucket: cfg.OutputBucket,
		AWSRegion:    cfg.AWSRegion,
		CDNDomain:    cfg.CDNDomain,
	}
	streamManager := live.NewStreamManager(store, managerCfg, logger)

	// Initialize SRT Handler
	srtHandler := live.NewSRTIngestHandler(&live.SRTConfig{
		ListenPort: cfg.SRTPortRangeStart,
	}, streamManager, nil, logger)

	apiHandler := live.NewAPIHandler(streamManager, srtHandler, logger)

	// Router
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		apiHandler.RegisterRoutes(r)
	})

	logger.Info("Live service starting", "port", cfg.Port)
	http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), r)
}

type Config struct {
	Port              int
	AWSRegion         string
	OutputBucket      string
	DynamoDBTable     string
	CDNDomain         string
	SRTPortRangeStart int
}

func loadConfig() *Config {
	return &Config{
		Port:          8081,
		AWSRegion:     os.Getenv("AWS_REGION"),
		OutputBucket:  os.Getenv("OUTPUT_BUCKET"),
		DynamoDBTable: os.Getenv("DYNAMODB_TABLE"),
		CDNDomain:     os.Getenv("CDN_DOMAIN"),
		SRTPortRangeStart: 9000,
	}
}
