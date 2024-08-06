package main

import (
	"fmt"
	"sort"
)

func main() {

	arr := []int{1, 2, 3, 4, 51, 23, 1, 345, 123, 1, 5, 61, 3, 12, 5, 12, 5, 12}

	sort.Slice(arr, func(i, j int) bool {
		return arr[i] < arr[j]
	})

	fmt.Println(arr)
}