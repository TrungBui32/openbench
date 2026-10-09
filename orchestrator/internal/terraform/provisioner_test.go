package terraform

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeleteHandoffVersions(t *testing.T) {
	t.Setenv(EnvStateBucket, "test-bucket")
	t.Setenv(EnvStateRegion, "us-east-1")
	key := "kubeconfig/run-1/join-token"
	deleted := map[string]bool{}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.Method {
		case "GET":
			body = `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>` + key + `</Key><VersionId>v1</VersionId></Version><Version><Key>` + key + `-other</Key><VersionId>other</VersionId></Version><DeleteMarker><Key>` + key + `</Key><VersionId>marker</VersionId></DeleteMarker></ListVersionsResult>`
		case "DELETE":
			if !strings.HasSuffix(r.URL.Path, key) {
				t.Fatalf("deleted unrelated key: %s", r.URL.Path)
			}
			deleted[r.URL.Query().Get("versionId")] = true
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: aws.AnonymousCredentials{}, HTTPClient: &http.Client{Transport: transport}})
	if err := (&AWS{s3: client}).deleteHandoff(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || !deleted["v1"] || !deleted["marker"] {
		t.Fatalf("versions not removed: %v", deleted)
	}
}

func TestDeleteHandoffReportsAccessFailure(t *testing.T) {
	t.Setenv(EnvStateBucket, "test-bucket")
	t.Setenv(EnvStateRegion, "us-east-1")
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("access denied") })
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: aws.AnonymousCredentials{}, HTTPClient: &http.Client{Transport: transport}}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err := (&AWS{s3: client}).deleteHandoff(context.Background(), "key"); err == nil {
		t.Fatal("cleanup failure ignored")
	}
}
