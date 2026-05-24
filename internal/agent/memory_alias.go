package agent

import "github.com/smasonuk/falken-core/internal/conversation"

type MemoryManager = conversation.MemoryManager
type MemoryUpdate = conversation.MemoryUpdate

var NewMemoryManager = conversation.NewMemoryManager
var RenderMemory = conversation.RenderMemory
var ValidateMemory = conversation.ValidateMemory
var NormalizeMemory = conversation.NormalizeMemory
var IsMemoryEmpty = conversation.IsMemoryEmpty
var MemoryEqual = conversation.MemoryEqual
