package secrets

import (
	"fmt"
	"os"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
)

// Resolver resolves the secrets declared in a job into name/value pairs for
// container injection. Values are only ever read from the process environment;
// nothing is written to logs, summaries, or Terraform state.
type Resolver struct{}

func New() *Resolver { return &Resolver{} }

// Resolve reads each declared secret from its declared source.
//
//   - source=env: reads the orchestrator's own environment variable named by
//     Key.
//   - source=jenkins-credentials: the Jenkins pipeline resolves the credential
//     and injects it into the environment under the credential ID before
//     invoking the CLI, so the orchestrator never talks to Jenkins' credential
//     store. Without that pre-seeded value this is an error.
func (r *Resolver) Resolve(secrets []config.Secret) (map[string]string, error) {
	out := make(map[string]string, len(secrets))
	for _, s := range secrets {
		switch s.Source {
		case "env", "jenkins-credentials":
			v, ok := os.LookupEnv(s.Key)
			if !ok || v == "" {
				return nil, fmt.Errorf(
					"secret %q: source %q requires environment variable %s to be set (jenkins-credentials is injected by the Jenkins pipeline before the CLI runs)",
					s.Name, s.Source, s.Key)
			}
			out[s.Name] = v
		default:
			return nil, fmt.Errorf("secret %q: unknown source %q", s.Name, s.Source)
		}
	}
	return out, nil
}
