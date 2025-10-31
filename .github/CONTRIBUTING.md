## Contributing

[fork]: /fork
[pr]: /compare
[dev-guide]: /DEVELOPER_GUIDE.md

Hi there! I'm thrilled that you'd like to contribute to this project. Your help is essential for keeping it great.

## Getting Started

**New to the codebase?** Check out our [Developer Guide][dev-guide] for:
- Architecture overview and design patterns
- Step-by-step instructions for adding new API calls
- How to add feature flags and output fields
- Testing and debugging tips
- Code examples and best practices

## Submitting a pull request

1. [Fork][fork] and clone the repository
1. Create a new branch: `git checkout -b my-branch-name`
1. Make your change following our [Developer Guide][dev-guide]
1. Test your changes:
   ```bash
   go test ./...
   go test -race ./...
   go build -o gh-stats
   ./gh-stats run --org test-org --verbose
   ```
1. Format and lint your code:
   ```bash
   go fmt ./...
   go vet ./...
   golangci-lint run
   ```
1. Push to your fork and [submit a pull request][pr]
1. Pat yourself on the back and wait for your pull request to be reviewed and merged.

Here are a few things you can do that will increase the likelihood of your pull request being accepted:

- Keep your change as focused as possible. If there are multiple changes you would like to make that are not dependent upon each other, consider submitting them as separate pull requests.
- Write a [good commit message](http://tbaggery.com/2008/04/19/a-note-about-git-commit-messages.html).
- Add tests for new functionality.
- Update documentation (README.md, godoc comments) as needed.
- Follow the patterns established in the [Developer Guide][dev-guide].

Work in Progress pull requests are also welcome to get feedback early on, or if there is something blocked you.

## Resources

- [How to Contribute to Open Source](https://opensource.guide/how-to-contribute/)
- [Using Pull Requests](https://help.github.com/articles/about-pull-requests/)
- [GitHub Help](https://help.github.com)
