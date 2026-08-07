// Package ttlwatcher provides a process that scans EC2 instances by their
// openbench:ttl_expires_at tag and force-terminates anything past its TTL — a
// crash-safety net if the orchestrator process dies mid-run.
package ttlwatcher

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

const ttlTag = "openbench:ttl_expires_at"

type Watcher struct {
	ec2 *ec2.Client
	log *log.Logger
}

func New(ctx context.Context, region string) (*Watcher, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &Watcher{ec2: ec2.NewFromConfig(cfg), log: log.Default()}, nil
}

// RunOnce terminates every instance whose ttl_expires_at tag is in the past.
// Returns the number terminated.
func (w *Watcher) RunOnce(ctx context.Context) (int, error) {
	var expired []string
	now := time.Now().UTC()

	// DescribeInstances paginates by default; walk every page so instances
	// past the first MaxResults are not missed.
	p := ec2.NewDescribeInstancesPaginator(w.ec2, &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{Name: aws.String("tag-key"), Values: []string{ttlTag}},
			{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped"}},
		},
	})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return len(expired), fmt.Errorf("describing instances: %w", err)
		}
		for _, res := range out.Reservations {
			for _, inst := range res.Instances {
				expiresAt := instanceTag(inst, ttlTag)
				if expiresAt == "" {
					continue
				}
				t, err := time.Parse(time.RFC3339, expiresAt)
				if err != nil {
					w.log.Printf("warning: instance %s has unparseable %s tag %q", *inst.InstanceId, ttlTag, expiresAt)
					continue
				}
				if t.Before(now) {
					expired = append(expired, *inst.InstanceId)
				}
			}
		}
	}

	for _, id := range expired {
		w.log.Printf("terminating expired instance %s (past TTL)", id)
		if _, err := w.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}}); err != nil {
			return len(expired), fmt.Errorf("terminating %s: %w", id, err)
		}
	}
	return len(expired), nil
}

// Watch runs RunOnce on an interval until ctx is cancelled.
func (w *Watcher) Watch(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := w.RunOnce(ctx)
		if err != nil {
			w.log.Printf("ttl watch error: %v", err)
		} else if n > 0 {
			w.log.Printf("terminated %d expired instance(s)", n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func instanceTag(inst types.Instance, key string) string {
	for _, t := range inst.Tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}
	return ""
}
