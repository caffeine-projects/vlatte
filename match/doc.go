// Package match provides request matchers, such as [Equal], [JSONPath], [All] and [Not].
// The mock builders of the protocol packages take them to match parts of a request.
//
// # How values compare
//
// Every matcher compares values by one rule, the same that mock files and settings read values by.
// A value has a class: nil, a bool, text (a string or a byte slice), a number (any Go int, uint or float),
// a list (another slice, or an array), an object (a map with string keys), or another value, such as a struct.
//
// Values of one class compare by value. Bools and text are equal when they are the same. Numbers are equal when
// their exact values are, whatever their Go types: 10, int64(10) and 10.0 are equal, 1.5 is never 1, and an int64
// above 2^53 is never the float64 nearest to it. Lists are equal item by item, in order, and objects member by member.
// Other values compare with reflect.DeepEqual.
//
// Most of a request is text: its path, query, headers and form values, and a body that isn't JSON. So two readings
// cross classes, for [Equal] and the matchers built on it, [IsIn], [Contain] and [ItemsMatch]:
//   - text compared with a number reads as a number when it is written in the JSON number grammar:
//     Equal(10) matches the query value "10" and "1e1", but not "010", "+10" or " 10";
//   - anything compared with a bool reads as one the way strconv.ParseBool reads text: Equal(true) matches true,
//     "true", "t", "1" and the number 1.
//
// Nothing else crosses classes: no number equals the character of its value, so Equal(49) doesn't match "1", and
// text never equals a list. Equal isn't transitive: "1.0" and "1" both equal 1, but not each other.
// [StrictEqual] and [EqualJSON] read no text at all, but still compare numbers by value: StrictEqual(1) matches a
// JSON 1.0, but not "1".
//
// The order matchers, such as [GreaterThan], compare numbers, and text that reads as one, by their exact values.
// [Truthy] and [Falsy] read bools the way Equal does; nil is neither true nor false. [Len] counts the bytes of text,
// the items of a list and the members of an object.
//
// Some matchers read text as a document: [EqualJSON] and [JSONSchema] as JSON, and [EqualXML] and [XPath] as XML.
// The date and time matchers, such as [Before], read text as a date and time, and compare instants, or wall clocks
// with [LocalTime]. Text they can't read doesn't match.
//
// A value of a class a matcher doesn't take, such as a number for [HasPrefix] or a missing field for [GreaterThan],
// doesn't match. It isn't an error: a matcher returns an error only when user code does, as in [Function], since an
// error stops the search for a mock.
package match
