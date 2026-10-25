// Discovery-only source fields. Port/execution/oracle state is mutable ledger
// bookkeeping and cannot change the immutable original test declaration.
export const sourceDeclarationFields = Object.freeze([
 'id','source_file','source_sha256','line','start','end','suite_path','title','full_name',
 'parameter_index','parameter','parameter_expression','contexts','source_state',
 'modifiers','kind','test_source','inputs','assertions','type_error_directives','expansion_status',
]);
export function sameOriginalDeclaration(actual,expected){
 return !!actual && !!expected && sourceDeclarationFields.every(key=>JSON.stringify(actual[key])===JSON.stringify(expected[key]));
}
export function assertOriginalDeclaration(actual,expected){
 if(!sameOriginalDeclaration(actual,expected))throw Error('Immutable original declaration or inputs changed: '+(expected?.id??'<missing>'));
}
