// Exact M40 additions. Every previous stage remains independently required.
export const indexingProfileStages = [
 'indexing-profile-source-postgres','indexing-profile-source-pglite',
 'indexing-profile-probes-postgres','indexing-profile-probes-pglite',
 'indexing-profile-fixture','indexing-profile-source-collect',
 'indexing-profile-probe-fixture','indexing-profile-evidence-controls',
];
export const indexingProfileHarness = [
 'tools/indexing-profile-acceptance.mjs','tools/indexing-profile-full-verify.mjs',
 'tools/indexing-profile-path-guards.test.mjs','tools/indexing-profile-collect-all.mjs',
 'tools/source-indexing-profile-results.mjs','tools/source-indexing-profile-map.mjs',
 'tools/source-indexing-profile-original.mjs','tools/source-indexing-profile-live.config.mjs','tools/source-indexing-profile-live.setup.ts',
 'tools/source-indexing-profile-fixture.mjs','tools/source-indexing-profile-collect.mjs',
 'tools/source-indexing-profile-evidence.mjs','tools/source-indexing-profile-evidence.test.mjs',
 'tools/source-indexing-profile-probes.mjs','tools/source-indexing-profile-probes.config.mjs','tools/source-indexing-profile-probes.test.ts',
 'tools/source-indexing-profile-probe-fixture.mjs',
];
export const indexingProfileSupplementalTests = [
 'TestSourceIndexingProfileEdges','TestProfilePrimitiveEquality',
 'TestSourceIndexingPrefetchProbes','TestClientProfileServiceContext',
 'TestClientPrefetchCloseOwnsFailedSibling','TestClientPrefetchReaderSingleSettlement',
];
export const indexingProfileStoreTests = [
 'TestSourceRPCAsyncReadAdmission','TestSourceRPCAsyncReadValidation',
 'TestSourceRPCAsyncReadCallerCancellation','TestSourceRPCAsyncReadBackends',
];
export const indexingProfileStoreScenarios = [
 'prepared_read_hit_and_miss','close_timeout_retains_read_ownership',
 'bound_adapters_no_async_reader','late_sql_read_error',
];
