package greeting

import "fmt"

func Greeting(name string) string {
	return fmt.Sprintf("Hi %s!", name)
}
