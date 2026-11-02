import { addProjectConfiguration, joinPathFragments } from '@nx/devkit'

export default async (tree, options) => {
  const root = options.directory.replace(/\\/g, '/').replace(/\/$/, '')
  if (!/^[a-zA-Z0-9_-]+$/.test(options.name) || !/^[a-zA-Z0-9_/-]+$/.test(root))
    throw new Error('Use safe project names and relative directory segments')
  if (
    root.startsWith('/') ||
    root.split('/').includes('..') ||
    tree.exists(`${root}/project.json`)
  )
    throw new Error('Choose an unused directory inside the workspace')
  const targets = {
    build: {
      executor: 'nx:run-commands',
      inputs: ['default', '^default', 'goGlobals'],
      options: {
        command: options.application
          ? `scripts/go build -trimpath -o dist/${options.name} ./${root}`
          : `scripts/go build ./${root}/...`
      },
      outputs: options.application
        ? [`{workspaceRoot}/dist/${options.name}`]
        : []
    },
    test: {
      executor: 'nx:run-commands',
      inputs: ['default', '^default', 'goGlobals'],
      options: { command: `scripts/go test -race -count=1 ./${root}/...` }
    },
    lint: {
      executor: 'nx:run-commands',
      inputs: ['default', '^default', 'goGlobals'],
      options: { command: `scripts/go vet ./${root}/...` }
    }
  }
  if (options.application)
    targets.serve = {
      executor: 'nx:run-commands',
      continuous: true,
      options: { command: `scripts/go run ./${root}` }
    }
  addProjectConfiguration(tree, options.name, {
    root,
    sourceRoot: root,
    projectType: options.application ? 'application' : 'library',
    tags: ['language:go'],
    implicitDependencies: options.dependencies,
    targets
  })
  tree.write(
    joinPathFragments(root, options.application ? 'main.go' : 'doc.go'),
    options.application
      ? 'package main\n\nfunc main() {}\n'
      : 'package ' + root.split('/').at(-1).replaceAll('-', '_') + '\n'
  )
}
