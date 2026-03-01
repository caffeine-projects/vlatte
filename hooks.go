package vlatte

// var ErrHookNotRegistered = errors.New("vlatte: hook not registered")

// type (
// 	OnComplete interface {
// 		Run(ctx context.Context, input OnCompleteInput) error
// 	}

// 	OnCompleteInput interface {
// 		Kind() Kind
// 		Bean() Bean
// 		Kit() Kit
// 		Parameters() any
// 	}

// 	OnCompleteDefinition struct {
// 		Name       string
// 		Parameters any
// 	}
// )

// type Hooks struct {
// 	mu         sync.RWMutex
// 	onComplete map[string]OnComplete
// }

// func NewHooks() *Hooks {
// 	return &Hooks{
// 		onComplete: make(map[string]OnComplete, 0),
// 	}
// }

// func (h *Hooks) OnComplete(name string, evt OnComplete) {
// 	h.mu.Lock()
// 	defer h.mu.Unlock()
// 	h.onComplete[name] = evt
// }
