package service

func resolveBillableImageCount(group *Group, outputCount int, requestedCount int) int {
	if !GroupUsesRequestedImageBillingCount(group) {
		return outputCount
	}
	if outputCount <= 0 {
		return outputCount
	}
	if requestedCount > 0 {
		return requestedCount
	}
	return outputCount
}
