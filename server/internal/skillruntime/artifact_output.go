package skillruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const submitArtifactToolName = "submit_artifact"
const maxArtifactBytes = 10 << 20

type artifactSubmitter struct {
	root, expectedPath string
	mu                 sync.Mutex
	dir                *os.Root
	content            string
	submitted          bool
}

func newArtifactSubmitter(root, expectedPath string) (*artifactSubmitter, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact root is required")
	}
	safePath, err := validateArtifactPath(expectedPath)
	if err != nil {
		return nil, fmt.Errorf("validate artifact path: %w", err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(filepath.Clean(absRoot))
	if err != nil {
		return nil, fmt.Errorf("resolve artifact root symlinks: %w", err)
	}
	info, err := os.Stat(realRoot)
	if err != nil {
		return nil, fmt.Errorf("stat artifact root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("artifact root must be a directory")
	}
	dir, err := os.OpenRoot(realRoot)
	if err != nil {
		return nil, fmt.Errorf("open artifact root: %w", err)
	}
	return &artifactSubmitter{root: realRoot, expectedPath: safePath, dir: dir}, nil
}

func (s *artifactSubmitter) Tool() tool.InvokableTool { return s }

func (s *artifactSubmitter) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == nil {
		return nil
	}
	err := s.dir.Close()
	s.dir = nil
	return err
}

func (s *artifactSubmitter) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: submitArtifactToolName,
		Desc: "Submit the completed artifact file from the current Skill Run. The path must exactly match the declared output_artifact path.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path": {
				Type:     schema.String,
				Desc:     "Relative path of the completed artifact, exactly as declared by output_artifact.",
				Enum:     []string{s.expectedPath},
				Required: true,
			},
		}),
	}, nil
}

func (s *artifactSubmitter) InvokableRun(_ context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	if s == nil {
		return "", errors.New("artifact submitter is not configured")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var args struct {
		Path string `json:"path"`
	}
	decoder := json.NewDecoder(strings.NewReader(argsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", submitArtifactToolName, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", submitArtifactToolName, err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("%s: path is required", submitArtifactToolName)
	}
	if args.Path != s.expectedPath {
		return "", fmt.Errorf("%s: path must exactly match %q", submitArtifactToolName, s.expectedPath)
	}
	if s.dir == nil {
		return "", fmt.Errorf("%s: artifact submitter is closed", submitArtifactToolName)
	}
	if s.submitted {
		return "artifact submitted", nil
	}

	info, err := s.dir.Lstat(s.expectedPath)
	if err != nil {
		return "", fmt.Errorf("%s: inspect artifact: %w", submitArtifactToolName, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s: artifact must not be a symlink", submitArtifactToolName)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s: artifact must be a regular file", submitArtifactToolName)
	}
	file, err := s.dir.Open(s.expectedPath)
	if err != nil {
		return "", fmt.Errorf("%s: open artifact: %w", submitArtifactToolName, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("%s: inspect opened artifact: %w", submitArtifactToolName, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return "", fmt.Errorf("%s: artifact changed while being submitted", submitArtifactToolName)
	}
	if openedInfo.Size() <= 0 {
		return "", fmt.Errorf("%s: artifact must not be empty", submitArtifactToolName)
	}
	if openedInfo.Size() > maxArtifactBytes {
		return "", fmt.Errorf("%s: artifact exceeds %d-byte limit", submitArtifactToolName, maxArtifactBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxArtifactBytes+1))
	if err != nil {
		return "", fmt.Errorf("%s: read artifact: %w", submitArtifactToolName, err)
	}
	if int64(len(raw)) != openedInfo.Size() {
		return "", fmt.Errorf("%s: artifact changed size while being submitted", submitArtifactToolName)
	}
	finalInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("%s: inspect submitted artifact: %w", submitArtifactToolName, err)
	}
	if !os.SameFile(openedInfo, finalInfo) || finalInfo.Size() != openedInfo.Size() {
		return "", fmt.Errorf("%s: artifact changed while being submitted", submitArtifactToolName)
	}

	s.content = string(raw)
	s.submitted = true
	return "artifact submitted", nil
}

func (s *artifactSubmitter) SubmittedContent() (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.content, s.submitted
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("multiple JSON values are not allowed")
}

var _ tool.InvokableTool = (*artifactSubmitter)(nil)
