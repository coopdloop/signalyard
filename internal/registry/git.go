package registry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// GitCommitter is the git-backed audit trail for schema changes (ADR-003).
// Every approved schema version becomes one commit containing the JSON Schema
// and routing YAML for the category.
type GitCommitter interface {
	CommitSchema(ctx context.Context, category string, version int, jsonSchema []byte, routingYAML, message string) (sha string, err error)
}

// NoopGit is used in dev when GIT_REPO_URL is unset: Postgres remains the
// source of truth and no audit commits are produced.
type NoopGit struct{}

func (NoopGit) CommitSchema(context.Context, string, int, []byte, string, string) (string, error) {
	return "", nil
}

type GoGit struct {
	repo   *git.Repository
	auth   transport.AuthMethod
	remote bool
}

// NewGoGit clones (or opens, for local paths) the schema repo into workDir.
func NewGoGit(ctx context.Context, repoURL, sshKeyPath, workDir string) (*GoGit, error) {
	if workDir == "" {
		var err error
		workDir, err = os.MkdirTemp("", "signalyard-schemas-*")
		if err != nil {
			return nil, err
		}
	}
	g := &GoGit{}

	if sshKeyPath != "" && strings.HasPrefix(repoURL, "git@") {
		key, err := os.ReadFile(sshKeyPath) //nolint:gosec // operator-supplied deploy key path from config
		if err != nil {
			return nil, fmt.Errorf("read ssh key: %w", err)
		}
		g.auth, err = ssh.NewPublicKeys("git", key, "")
		if err != nil {
			return nil, fmt.Errorf("parse ssh key: %w", err)
		}
	}

	isLocal := !strings.Contains(repoURL, "://") && !strings.HasPrefix(repoURL, "git@")
	if isLocal {
		repo, err := git.PlainOpen(repoURL)
		if err != nil {
			return nil, fmt.Errorf("open local schema repo %s: %w", repoURL, err)
		}
		g.repo = repo
		return g, nil
	}

	repo, err := git.PlainCloneContext(ctx, workDir, false, &git.CloneOptions{URL: repoURL, Auth: g.auth})
	if err != nil {
		return nil, fmt.Errorf("clone schema repo: %w", err)
	}
	g.repo = repo
	g.remote = true
	return g, nil
}

// safeCategory rejects category names that could escape the schemas/ directory.
func safeCategory(category string) (string, error) {
	if category == "" || category != filepath.Base(category) ||
		strings.ContainsAny(category, `/\`) || strings.Contains(category, "..") {
		return "", fmt.Errorf("invalid schema category %q", category)
	}
	return category, nil
}

func (g *GoGit) CommitSchema(_ context.Context, category string, version int, jsonSchema []byte, routingYAML, message string) (string, error) {
	category, err := safeCategory(category)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(repoWorkDir(g.repo), "schemas")
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // dir is repo-relative, category validated above
		return "", err
	}
	schemaFile := filepath.Join(dir, category+".schema.json")
	routingFile := filepath.Join(dir, category+".routing.yaml")
	if err := os.WriteFile(schemaFile, jsonSchema, 0o600); err != nil { //nolint:gosec // category validated by safeCategory
		return "", err
	}
	if err := os.WriteFile(routingFile, []byte(routingYAML), 0o600); err != nil { //nolint:gosec // category validated by safeCategory
		return "", err
	}

	wt, err := g.repo.Worktree()
	if err != nil {
		return "", err
	}
	if _, err := wt.Add(filepath.Join("schemas", category+".schema.json")); err != nil {
		return "", err
	}
	if _, err := wt.Add(filepath.Join("schemas", category+".routing.yaml")); err != nil {
		return "", err
	}
	if message == "" {
		message = fmt.Sprintf("schema(%s): version %d", category, version)
	}
	commit, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{Name: "signal-yard schema-registry", Email: "schema-registry@signalyard.local", When: time.Now()},
	})
	if err != nil {
		return "", err
	}
	if g.remote {
		if err := g.repo.Push(&git.PushOptions{Auth: g.auth}); err != nil {
			return "", fmt.Errorf("push schema commit: %w", err)
		}
	}
	return commit.String(), nil
}

func repoWorkDir(repo *git.Repository) string {
	wt, err := repo.Worktree()
	if err != nil {
		return ""
	}
	return wt.Filesystem.Root()
}

// NewGitCommitter picks the git backing based on config.
func NewGitCommitter(ctx context.Context, cfg *Config) (GitCommitter, error) {
	if cfg.GitRepoURL == "" {
		slog.Warn("GIT_REPO_URL not set: schema changes will not be committed to git (dev mode)")
		return NoopGit{}, nil
	}
	return NewGoGit(ctx, cfg.GitRepoURL, cfg.GitSSHKeyPath, cfg.GitWorkDir)
}
