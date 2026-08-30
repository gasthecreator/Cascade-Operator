// Command payments is one of the two leaf dependencies in the §2.7 demo
// topology (checkout -> {payments, inventory}). It has no logic of its own
// beyond depsvc's toggleable health.
package main

import (
	"log"

	"github.com/gasthecreator/Cascade-Operator/demo/internal/depsvc"
)

func main() {
	if err := depsvc.Run("payments", ":8080"); err != nil {
		log.Fatal(err)
	}
}
