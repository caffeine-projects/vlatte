package handler

// MockSchema returns the JSON Schema of the server's mock files, a JSON document, so a client writes mocks the server
// takes. It needs a server that describes its mock files.
func (o *Operations) MockSchema() ([]byte, error) {
	if o.schema == nil {
		return nil, notSupported(_lackingSchema)
	}

	return o.schema, nil
}
