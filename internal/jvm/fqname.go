package jvm

import "strings"

// fqName builds the canonical "<class>#<method>" identity used to join a
// test across report sources (Allure ↔ testng-results.xml). Returns "" if
// either part is missing so the caller can fall back.
func fqName(class, method string) string {
	if class == "" || method == "" {
		return ""
	}
	return class + "#" + method
}

// fqNameFromFullName derives "<class>#<method>" from an Allure `fullName`
// like "api.pd.PdBackendE2ETest.traderCreatesWallet". The method is the
// last dot-separated segment (Allure's convention for TestNG/JUnit), so we
// split on the final dot. A fullName with no dot (unusual) is returned
// unchanged as a best-effort identity.
func fqNameFromFullName(full string) string {
	full = strings.TrimSpace(full)
	if full == "" {
		return ""
	}
	i := strings.LastIndex(full, ".")
	if i <= 0 || i == len(full)-1 {
		return full
	}
	return full[:i] + "#" + full[i+1:]
}

// methodOf returns the method segment of a "<class>#<method>" fq_name, or
// the whole string when there is no separator.
func methodOf(fq string) string {
	if i := strings.LastIndex(fq, "#"); i >= 0 {
		return fq[i+1:]
	}
	return fq
}
