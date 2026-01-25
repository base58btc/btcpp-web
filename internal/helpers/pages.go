package helpers

import (
        "fmt"
)

type LoginPage struct {
	Year        uint
	Destination string
}


func ErrTalkApp(message string) string {
        return fmt.Sprintf(`
        <div class="form_message-error" style="display: block;">
          <div class="error-text text-red-700">%s. Try again or email us at <a href="mailto:speak@btcpp.dev>speak@btcpp.dev</a>.</div>
        </div>
        `, message)
}
