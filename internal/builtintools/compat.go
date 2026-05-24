package builtintools

import "github.com/smasonuk/falken-core/internal/builtintools/api"

type Tool = api.Tool
type Descriptor = api.Descriptor
type Safety = api.Safety
type Host = api.Host

var ErrHostUnavailable = api.ErrHostUnavailable

var NewHost = api.NewHost

var Success = api.Success
var ResultFromPayload = api.ResultFromPayload
var Fail = api.Fail
var DecodeArgs = api.DecodeArgs
var DecodeStrictJSON = api.DecodeStrictJSON

var MustSchema = api.MustSchema
var ObjectSchema = api.ObjectSchema
var StringProp = api.StringProp
var IntegerProp = api.IntegerProp
var BoolProp = api.BoolProp
var StringEnumProp = api.StringEnumProp
var ArrayProp = api.ArrayProp
var ObjectProp = api.ObjectProp
var StringMapProp = api.StringMapProp
