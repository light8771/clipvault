import { parse } from 'acorn';

const escapes = new Set(['app.esc', 'escapeHTML', 'escapeText']);
const sinks = new Set(['innerHTML', 'outerHTML']);
const isFunction = n => /^(?:FunctionExpression|FunctionDeclaration|ArrowFunctionExpression)$/.test(n.type);
const memberKey = n => n?.type !== 'MemberExpression' ? '' : n.computed
  ? (n.property.type === 'Literal' ? n.property.value : '') : n.property.name;
const nameOf = n => n?.type === 'Identifier' ? n.name
  : memberKey(n) ? `${nameOf(n.object)}.${memberKey(n)}` : '';
function children(node) {
  return Object.values(node).flatMap(value => Array.isArray(value) ? value : [value])
    .filter(value => value && typeof value.type === 'string');
}

// Syntax-aware, intra-file analysis: follow local bindings, assignments and
// function returns into HTML, ignoring condition predicates and escaped values.
// Imported/dynamically assigned helpers still require rendering tests and review.
export function unsafeHTMLTranslations(source) {
  const tree = parse(source, { ecmaVersion: 'latest', sourceType: 'module', locations: true });
  const scopes = new WeakMap();
  const nodes = [];
  const root = { parent: null, definitions: new Map() };
  function define(scope, name, expression) {
    if (!name || !expression) return;
    const values = scope.definitions.get(name) || [];
    values.push(expression);
    scope.definitions.set(name, values);
  }
  function bindingScope(scope, name) {
    for (let current = scope; current; current = current.parent) {
      if (current.definitions.has(name)) return current;
    }
    return scope;
  }
  function index(node, scope) {
    if (node.type === 'FunctionDeclaration') define(scope, node.id.name, node);
    if (isFunction(node) || node.type === 'BlockStatement') {
      scope = { parent: scope, definitions: new Map() };
      if (isFunction(node)) for (const param of node.params) {
        if (param.type === 'Identifier') scope.definitions.set(param.name, []);
      }
    }
    scopes.set(node, scope);
    nodes.push(node);
    if (node.type === 'VariableDeclarator') {
      if (node.id.type === 'Identifier') {
        if (!scope.definitions.has(node.id.name)) scope.definitions.set(node.id.name, []);
        define(scope, node.id.name, node.init);
      }
    }
    for (const child of children(node)) index(child, scope);
  }
  index(tree, root);
  for (const node of nodes) {
    if (node.type === 'AssignmentExpression') {
      const name = nameOf(node.left);
      define(bindingScope(scopes.get(node), name), name, node.right);
    }
  }
  function resolve(node) {
    const name = nameOf(node);
    const scope = bindingScope(scopes.get(node), name);
    return scope.definitions.get(name) || [];
  }
  function returns(node) {
    if (node.type === 'ArrowFunctionExpression' && node.body.type !== 'BlockStatement') return [node.body];
    const result = [];
    function visit(n) {
      if (isFunction(n)) return;
      if (n.type === 'ReturnStatement' && n.argument) result.push(n.argument);
      else for (const child of children(n)) visit(child);
    }
    visit(node.body);
    return result;
  }
  function isTranslator(node, seen = new Set()) {
    if (seen.has(node)) return false;
    const name = nameOf(node);
    if (name === 't' || name === 'window.clipvaultI18n.t') return true;
    const next = new Set(seen).add(node);
    return resolve(node).some(value => isTranslator(value, next));
  }
  function unsafe(node, seen = new Set()) {
    if (!node || seen.has(node)) return false;
    const next = new Set(seen).add(node);
    const any = values => values.some(value => unsafe(value, next));
    if (node.type === 'CallExpression') {
      const name = nameOf(node.callee);
      if (escapes.has(name)) return false;
      if (isTranslator(node.callee)) return true;
      return any(resolve(node.callee)) || any(node.arguments);
    }
    if (node.type === 'Identifier') return any(resolve(node));
    if (node.type === 'MemberExpression') return any(resolve(node)) || unsafe(node.object, next);
    if (node.type === 'ConditionalExpression') return any([node.consequent, node.alternate]);
    if (isFunction(node)) return any(returns(node));
    if (node.type === 'TemplateLiteral') return any(node.expressions);
    if (node.type === 'BinaryExpression' || node.type === 'LogicalExpression') return any([node.left, node.right]);
    if (node.type === 'ArrayExpression') return any(node.elements);
    if (node.type === 'ObjectExpression') return any(node.properties.map(property => property.value));
    if (node.type === 'SequenceExpression') return unsafe(node.expressions.at(-1), next);
    if (node.type === 'ChainExpression') return unsafe(node.expression, next);
    return false;
  }
  function hasMarkup(node) {
    if (node.type === 'Literal') return typeof node.value === 'string' && /<\/?[a-z][\w-]*(?:\s|>)/i.test(node.value);
    if (node.type === 'TemplateLiteral') return node.quasis.some(part => /<\/?[a-z][\w-]*(?:\s|>)/i.test(part.value.raw));
    if (node.type === 'BinaryExpression' && node.operator === '+') return hasMarkup(node.left) || hasMarkup(node.right);
    return false;
  }
  const lines = new Set();
  for (const node of nodes) {
    let expression;
    if (node.type === 'AssignmentExpression' && sinks.has(memberKey(node.left))) expression = node.right;
    else if (node.type === 'CallExpression' && memberKey(node.callee) === 'insertAdjacentHTML') expression = node.arguments[1];
    else if (hasMarkup(node)) expression = node;
    if (expression && unsafe(expression)) lines.add(node.loc.start.line);
  }
  return [...lines].sort((a, b) => a - b);
}
