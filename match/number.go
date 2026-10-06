package match

// Number is any Go integer or floating-point type: the type of the bound of an order matcher, such as GreaterThan.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr | ~float32 | ~float64
}
