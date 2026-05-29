package interbank

import "encoding/json"

// jsonUnmarshal is split out so a test can swap in a strict decoder.
var jsonUnmarshal = json.Unmarshal
