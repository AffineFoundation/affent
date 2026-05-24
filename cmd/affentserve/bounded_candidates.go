package main

import "sort"

func addBoundedStringCandidate(candidates map[string]struct{}, value string, cap int) {
	if cap <= 0 {
		return
	}
	candidates[value] = struct{}{}
	for len(candidates) > cap {
		var highest string
		for value := range candidates {
			if highest == "" || value > highest {
				highest = value
			}
		}
		delete(candidates, highest)
	}
}

func sortedStringCandidates(candidates map[string]struct{}) []string {
	values := make([]string, 0, len(candidates))
	for value := range candidates {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
