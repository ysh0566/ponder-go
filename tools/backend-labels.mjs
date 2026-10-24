// Source test titles may mention another backend. The first explicit dialect
// component identifies the actual native backend context.
export function backendLabelMatches(testName, backend) {
	const name = testName.toLowerCase();
	const explicit = name.split("/").find(part => part === "postgres" || part === "pglite");
	return explicit ? explicit === backend : name.includes(backend);
}
