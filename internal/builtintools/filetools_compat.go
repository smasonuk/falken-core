package builtintools

import "github.com/smasonuk/falken-core/internal/builtintools/filetools"

type ReadFileTool = filetools.ReadFileTool
type ReadFilesTool = filetools.ReadFilesTool
type GlobTool = filetools.GlobTool
type GrepTool = filetools.GrepTool
type WriteFileTool = filetools.WriteFileTool
type EditFileTool = filetools.EditFileTool
type MultiEditTool = filetools.MultiEditTool
type ApplyPatchTool = filetools.ApplyPatchTool
type DeleteFileTool = filetools.DeleteFileTool

var ReadFileProps = filetools.ReadFileProps
var EditFileProps = filetools.EditFileProps
