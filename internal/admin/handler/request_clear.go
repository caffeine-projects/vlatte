package handler

// ClearRequests removes every request of the journal. It needs a server that keeps a journal.
func (o *Operations) ClearRequests() error {
	if o.journal == nil {
		return notSupported(_lackingJournal)
	}

	return o.journal.Clear()
}
