package host

import "github.com/smasonuk/falken-core/pkg/workspacepath"

type pathResolver struct{}

func (r *pathResolver) resolvePath(s *StatefulShell, path string) (string, error) {
	return workspacepath.Resolve(s.WorkspaceDir, s.RealCWD, path)
}
