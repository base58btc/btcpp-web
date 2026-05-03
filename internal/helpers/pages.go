package helpers

import (
        "fmt"
)

type LoginPage struct {
	Year        uint
	Destination string
}


func ErrSpeakerApp(message string) string {
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

func SuccessApp(message string) string {
        return fmt.Sprintf(`
        <div class="form_message-success rounded-md border border-green-300 bg-green-50 p-4" style="display: block;" role="status" aria-live="polite">
          <div class="text-green-800 font-semibold text-base">%s</div>
        </div>
        <script>(function(){var f=document.querySelector('form');if(f){f.reset();}})();</script>
        `, message)
}
