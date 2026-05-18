package cmd

import "fmt"

func requirePositiveLimit(flag string, value int) error {
	if value < 1 {
		return fmt.Errorf("%s must be at least 1, got %d", flag, value)
	}
	return nil
}

func requireNonNegativeFloat(flag string, value float64) error {
	if value < 0 {
		return fmt.Errorf("%s must be non-negative, got %g", flag, value)
	}
	return nil
}
