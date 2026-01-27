package helpers

import (
        "fmt"
)

type LoginPage struct {
	Year        uint
	Destination string
}


func ErrTalkApp(message string) string {
        return ErrApp(message, "speak")        
}

func ErrVolApp(message string) string {
        return ErrApp(message, "volunteer")
}

func ErrApp(message, email string) string {
        return fmt.Sprintf(`
        <div class="form_message-error" style="display: block;">
          <div class="error-text text-red-700">%s Try again or email us at <a href="mailto:%s@btcpp.dev">%s@btcpp.dev</a>.</div>
        </div>
        `, message, email, email)
}
