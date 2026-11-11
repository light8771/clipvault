package ocr

// MessageError 只携带公开的词条键与参数，由调用方按当前界面语言展示。
type MessageError struct {
	Key    string
	Params map[string]any
}

func (e *MessageError) Error() string { return e.Key }
