// Package fakefy makes fake values that look like real data: from a JSON Schema, and from the name of the field the
// schema describes. A value it returns validates against the schema it was made from, as JSON, unless the schema uses
// a keyword fakefy doesn't read: not, if, then, else, contains, propertyNames, dependentRequired, dependentSchemas,
// unevaluatedItems and unevaluatedProperties, and patternProperties but for the required properties they name. A
// oneOf whose branches overlap may also make a value two of them match, which a schema fakefy checks its values
// against refuses, so it fails with [ErrUnsatisfiable] instead.
//
// A [Faker] given a seed makes the same values in the same order, so tools that write fake data to files, such as mock
// generators, write the same bytes on every run.
package fakefy
