package rdm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrBadStatusMessages is returned when STATUS_MESSAGES data isn't a whole
// number of 9-byte groups.
var ErrBadStatusMessages = errors.New("rdm: malformed STATUS_MESSAGES data")

// statusMessageGroupSize is one STATUS_MESSAGES repeated-group entry: 9
// bytes (report §2.4).
const statusMessageGroupSize = 9

// StatusMessage is one entry in STATUS_MESSAGES' repeated-group GET
// response (report §2.4):
//
//	sub_device   UINT16 BE
//	status_type  UINT8 (StatusType — severity of *this* message)
//	message_id   UINT16 BE
//	value1       INT16 BE
//	value2       INT16 BE
type StatusMessage struct {
	SubDevice uint16
	Type      StatusType
	MessageID uint16
	Value1    int16
	Value2    int16
}

// DecodeStatusMessages parses a STATUS_MESSAGES GET response (a flat
// repeated-group list; ACK_OVERFLOW concatenation, if any, is the
// controller's job — see session.RDMController — not this codec's).
func DecodeStatusMessages(data []byte) ([]StatusMessage, error) {
	if len(data)%statusMessageGroupSize != 0 {
		return nil, fmt.Errorf("%w: length %d not a multiple of %d", ErrBadStatusMessages, len(data), statusMessageGroupSize)
	}
	n := len(data) / statusMessageGroupSize
	out := make([]StatusMessage, 0, n)
	for i := 0; i < n; i++ {
		g := data[i*statusMessageGroupSize : (i+1)*statusMessageGroupSize]
		out = append(out, StatusMessage{
			SubDevice: binary.BigEndian.Uint16(g[0:2]),
			Type:      StatusType(g[2]),
			MessageID: binary.BigEndian.Uint16(g[3:5]),
			Value1:    int16(binary.BigEndian.Uint16(g[5:7])),
			Value2:    int16(binary.BigEndian.Uint16(g[7:9])),
		})
	}
	return out, nil
}

// EncodeStatusMessages is the inverse of DecodeStatusMessages.
func EncodeStatusMessages(msgs []StatusMessage) []byte {
	b := make([]byte, 0, len(msgs)*statusMessageGroupSize)
	for _, m := range msgs {
		g := make([]byte, statusMessageGroupSize)
		binary.BigEndian.PutUint16(g[0:2], m.SubDevice)
		g[2] = byte(m.Type)
		binary.BigEndian.PutUint16(g[3:5], m.MessageID)
		binary.BigEndian.PutUint16(g[5:7], uint16(m.Value1))
		binary.BigEndian.PutUint16(g[7:9], uint16(m.Value2))
		b = append(b, g...)
	}
	return b
}

// EncodeStatusMessagesRequest builds STATUS_MESSAGES' 1-byte GET request
// (the status_type severity filter).
func EncodeStatusMessagesRequest(filter StatusType) []byte {
	return []byte{byte(filter)}
}

// EncodeQueuedMessageRequest builds QUEUED_MESSAGE's (0x0020) 1-byte GET
// request (report §2.4: "drain your queue down to this severity or worse").
// QUEUED_MESSAGE has no GET response shape of its own — the responder
// answers with whatever queued PID it is draining (typically
// STATUS_MESSAGES), addressed as a spontaneous GET_COMMAND_RESPONSE for
// that PID; decode the response with that PID's own decoder.
func EncodeQueuedMessageRequest(filter StatusType) []byte {
	return []byte{byte(filter)}
}

// EncodeStatusIDDescriptionRequest builds STATUS_ID_DESCRIPTION's (0x0031)
// 2-byte GET request: one of the message_id values seen in a
// StatusMessage.
func EncodeStatusIDDescriptionRequest(statusID uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, statusID)
	return b
}

// DecodeStatusIDDescriptionResponse decodes STATUS_ID_DESCRIPTION's GET
// response: a bare ASCII label, <=32 bytes, no NUL terminator.
func DecodeStatusIDDescriptionResponse(data []byte) string {
	return string(data)
}
