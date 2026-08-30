// Command inventory is the other leaf dependency in the §2.7 demo topology
// (checkout -> {payments, inventory}). Same shape as payments — the two
// exist as separate binaries so checkout genuinely fans out to two
// distinct services, not one service under two names.
package main

import (
	"log"

	"github.com/gasthecreator/Cascade-Operator/demo/internal/depsvc"
)

func main() {
	if err := depsvc.Run("inventory", ":8080"); err != nil {
		log.Fatal(err)
	}
}
