// Package homebrewformula tests scripts/homebrew_formula.sh, the generator the
// release workflow renders jzills/homebrew-tap's Formula/kx.rb from. It builds
// nothing; its tests run the real script against a fabricated SHA256SUMS and
// read what it prints.
package homebrewformula
