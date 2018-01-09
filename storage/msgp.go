package storage

// NOTE: THIS FILE WAS PRODUCED BY THE
// MSGP CODE GENERATION TOOL (github.com/tinylib/msgp)
// DO NOT EDIT

import (
	"github.com/tinylib/msgp/msgp"
)

// MarshalMsg implements msgp.Marshaler
func (z SerializedPiece) MarshalMsg(b []byte) (o []byte, err error) {
	o = msgp.Require(b, z.Msgsize())
	// map header, size 3
	// string "I"
	o = append(o, 0x83, 0xa1, 0x49)
	o = msgp.AppendInt(o, z.I)
	// string "H"
	o = append(o, 0xa1, 0x48)
	o = msgp.AppendString(o, z.H)
	// string "C"
	o = append(o, 0xa1, 0x43)
	o = msgp.AppendBool(o, z.C)
	return
}

// UnmarshalMsg implements msgp.Unmarshaler
func (z *SerializedPiece) UnmarshalMsg(bts []byte) (o []byte, err error) {
	var field []byte
	_ = field
	var zb0001 uint32
	zb0001, bts, err = msgp.ReadMapHeaderBytes(bts)
	if err != nil {
		return
	}
	for zb0001 > 0 {
		zb0001--
		field, bts, err = msgp.ReadMapKeyZC(bts)
		if err != nil {
			return
		}
		switch msgp.UnsafeString(field) {
		case "I":
			z.I, bts, err = msgp.ReadIntBytes(bts)
			if err != nil {
				return
			}
		case "H":
			z.H, bts, err = msgp.ReadStringBytes(bts)
			if err != nil {
				return
			}
		case "C":
			z.C, bts, err = msgp.ReadBoolBytes(bts)
			if err != nil {
				return
			}
		default:
			bts, err = msgp.Skip(bts)
			if err != nil {
				return
			}
		}
	}
	o = bts
	return
}

// Msgsize returns an upper bound estimate of the number of bytes occupied by the serialized message
func (z SerializedPiece) Msgsize() (s int) {
	s = 1 + 2 + msgp.IntSize + 2 + msgp.StringPrefixSize + len(z.H) + 2 + msgp.BoolSize
	return
}
