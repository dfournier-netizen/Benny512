package rdm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrBadProductDetailIDList is returned when PRODUCT_DETAIL_ID_LIST data
// isn't a whole number of 2-byte IDs, or exceeds the max_size=6 bound.
var ErrBadProductDetailIDList = errors.New("rdm: malformed PRODUCT_DETAIL_ID_LIST data")

// MaxProductDetailIDs is PRODUCT_DETAIL_ID_LIST's max_size (report §2.3,
// CONFIRMED via OLA schema: "max_size: 6").
const MaxProductDetailIDs = 6

// DecodeProductDetailIDList parses PRODUCT_DETAIL_ID_LIST's (0x0070) GET
// response: a repeated group of up to 6 uint16 detail IDs.
func DecodeProductDetailIDList(data []byte) ([]ProductDetail, error) {
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("%w: odd length %d", ErrBadProductDetailIDList, len(data))
	}
	n := len(data) / 2
	if n > MaxProductDetailIDs {
		return nil, fmt.Errorf("%w: %d entries exceeds max %d", ErrBadProductDetailIDList, n, MaxProductDetailIDs)
	}
	out := make([]ProductDetail, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ProductDetail(binary.BigEndian.Uint16(data[i*2:i*2+2])))
	}
	return out, nil
}

// EncodeProductDetailIDList is the inverse of DecodeProductDetailIDList,
// clamping to the first MaxProductDetailIDs entries.
func EncodeProductDetailIDList(ids []ProductDetail) []byte {
	if len(ids) > MaxProductDetailIDs {
		ids = ids[:MaxProductDetailIDs]
	}
	b := make([]byte, len(ids)*2)
	for i, id := range ids {
		binary.BigEndian.PutUint16(b[i*2:i*2+2], uint16(id))
	}
	return b
}
