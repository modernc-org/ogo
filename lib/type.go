package octogo // import "octogo.dev/octogo/lib"

// Kind describes a Type
type Kind int

// Values of type Kind
const (
	PredefinedBool = iota
	PredefinedByte
	PredefinedInt
	PredefinedInt8
	PredefinedUint8
	PredefinedInt16
	PredefinedUint16
	PredefinedInt32
	PredefinedUint32
)
