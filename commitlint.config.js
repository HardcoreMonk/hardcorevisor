// Conventional Commits 규약 강제
// type(scope): description
// 허용 타입: feat, fix, docs, style, refactor, perf, test, build, ci, chore, deps
export default {
  extends: ['@commitlint/config-conventional'],
  rules: {
    'type-enum': [2, 'always', [
      'feat', 'fix', 'docs', 'style', 'refactor', 'perf',
      'test', 'build', 'ci', 'chore', 'deps', 'revert',
    ]],
    'subject-max-length': [2, 'always', 100],
    'body-max-line-length': [1, 'always', 200],
  },
}
