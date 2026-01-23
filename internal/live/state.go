package live

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/amillerrr/hls-pipeline/pkg/models"
)

// StreamStore defines the data access layer for live streams.
type StreamStore interface {
	SaveStream(ctx context.Context, stream *Stream) error
	GetStream(ctx context.Context, id string) (*Stream, error)
	UpdateState(ctx context.Context, id string, state StreamState, outputURL string) error
	ListStreams(ctx context.Context) ([]*Stream, error)
	DeleteStream(ctx context.Context, id string) error
}

type DynamoDBStore struct {
	client    *dynamodb.Client
	tableName string
}

func NewDynamoDBStore(client *dynamodb.Client, tableName string) *DynamoDBStore {
	return &DynamoDBStore{
		client:    client,
		tableName: tableName,
	}
}

func (s *DynamoDBStore) SaveStream(ctx context.Context, stream *Stream) error {
	item, err := attributevalue.MarshalMap(stream)
	if err != nil {
		return fmt.Errorf("failed to marshal stream: %w", err)
	}

	// Add PK/SK for DynamoDB access patterns
	item["pk"] = &types.AttributeValueMemberS{Value: fmt.Sprintf("STREAM#%s", stream.ID)}
	item["sk"] = &types.AttributeValueMemberS{Value: "METADATA"}
	item["gsi1pk"] = &types.AttributeValueMemberS{Value: "ALL_STREAMS"} // For listing
	item["gsi1sk"] = &types.AttributeValueMemberS{Value: stream.CreatedAt.Format(time.RFC3339)}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      item,
	})
	return err
}

func (s *DynamoDBStore) GetStream(ctx context.Context, id string) (*Stream, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("STREAM#%s", id)},
			"sk": &types.AttributeValueMemberS{Value: "METADATA"},
		},
	})
	if err != nil {
		return nil, err
	}
	if out.Item == nil {
		return nil, fmt.Errorf("stream not found")
	}

	var stream Stream
	if err := attributevalue.UnmarshalMap(out.Item, &stream); err != nil {
		return nil, err
	}
	return &stream, nil
}

func (s *DynamoDBStore) UpdateState(ctx context.Context, id string, state StreamState, outputURL string) error {
	updateExpr := "SET #state = :state"
	attrValues := map[string]types.AttributeValue{
		":state": &types.AttributeValueMemberS{Value: string(state)},
	}

	if outputURL != "" {
		updateExpr += ", outputUrl = :url"
		attrValues[":url"] = &types.AttributeValueMemberS{Value: outputURL}
	}

	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("STREAM#%s", id)},
			"sk": &types.AttributeValueMemberS{Value: "METADATA"},
		},
		UpdateExpression:          aws.String(updateExpr),
		ExpressionAttributeNames:  map[string]string{"#state": "state"},
		ExpressionAttributeValues: attrValues,
	})
	return err
}

func (s *DynamoDBStore) ListStreams(ctx context.Context) ([]*Stream, error) {
	// Query GSI1 to get all streams sorted by creation date
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("gsi1pk = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: "ALL_STREAMS"},
		},
	})
	if err != nil {
		return nil, err
	}

	var streams []*Stream
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &streams); err != nil {
		return nil, err
	}
	return streams, nil
}

func (s *DynamoDBStore) DeleteStream(ctx context.Context, id string) error {
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("STREAM#%s", id)},
			"sk": &types.AttributeValueMemberS{Value: "METADATA"},
		},
	})
	return err
}
