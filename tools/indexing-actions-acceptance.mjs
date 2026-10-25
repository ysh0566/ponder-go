// One explicit M39 stage set. Older collectors still reject unknown/duplicate stages.
export const indexingActionsStages = [
 'indexing-actions-source-postgres','indexing-actions-source-pglite',
 'indexing-actions-probes-postgres','indexing-actions-probes-pglite',
 'indexing-actions-fixture','indexing-actions-source-collect','indexing-actions-evidence-controls',
 'indexing-actions-probe-fixture',
];
export const indexingActionsHarness = [
 'tools/indexing-actions-path-guards.test.mjs','tools/indexing-actions-acceptance.mjs','tools/indexing-actions-full-verify.mjs',
 'tools/source-indexing-actions-results.mjs','tools/source-indexing-actions-map.mjs',
 'tools/source-indexing-actions-original.mjs','tools/source-indexing-actions-live.config.mjs','tools/source-indexing-actions-live.setup.ts',
 'tools/source-indexing-actions-fixture.mjs','tools/source-indexing-actions-collect.mjs',
 'tools/source-indexing-actions-evidence.mjs','tools/source-indexing-actions-evidence.test.mjs',
 'tools/source-indexing-actions-probes.mjs','tools/source-indexing-actions-probes.config.mjs','tools/source-indexing-actions-probes.test.ts',
 'tools/source-indexing-actions-probe-fixture.mjs',
];
export const indexingActionsSupplementalTests = [
 'TestSourceIndexingCacheKeyBoundary','TestClientRealCacheAdmissionLifecycle','TestStrictClientRejectsSynchronousOnlyCache',
 'TestActionEmptyRetryPolicy','TestActionCacheCanonicalizationAndEmptyExclusion',
 'TestContractABIEncodingAndSelection','TestContractABIAmbiguousOverloads','TestContractABITupleArraysAndSignedBounds',
 'TestContractABIResultShapesAndZeroData','TestContractABIUnnamedTupleResult','TestContractABINumericWidthsAndInputSnapshot',
 'TestSourceIndexingActionsProbes','TestSourceIndexingServiceProxy',
];
export const indexingActionsAdmissionScenarios = ['success','late-error','caller-cancel','client-close','adapter-close','business-rollback'];
export const indexingActionsStoreTests = ['TestSourceRPCAsyncAdmission','TestSourceRPCAsyncValidation','TestSourceRPCAsyncCallerCancellation','TestSourceRPCAsyncBackends'];
export const indexingActionsStoreScenarios = ['prepared_snapshot_and_real_rows','delayed_write_native_read_visibility','close_timeout_retains_ownership_and_rolls_back','bound_adapters_remain_synchronous_and_rollback','late_real_sql_error'];
