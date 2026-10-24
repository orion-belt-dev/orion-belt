// @ts-check
import {themes as prismThemes} from 'prism-react-renderer';

/** @type {import('@docusaurus/types').Config} */
const config = {
  themes: ['@docusaurus/theme-mermaid'],
  title: 'Orion Belt',
  tagline: 'Self-hosted SSH access gateway with PAM workflows',
  favicon: 'favicon.ico',
  headTags: [
    {tagName: 'link', attributes: {rel: 'icon', href: '/favicon.svg', type: 'image/svg+xml'}},
    {tagName: 'link', attributes: {rel: 'apple-touch-icon', href: '/apple-touch-icon.png'}},
    {tagName: 'link', attributes: {rel: 'manifest', href: '/site.webmanifest'}},
    {tagName: 'meta', attributes: {name: 'theme-color', content: '#0A0F1C'}},
    {
      tagName: 'link',
      attributes: {rel: 'preload', href: '/fonts/Geist-Regular.ttf', as: 'font', type: 'font/ttf', crossorigin: 'anonymous'},
    },
    {
      tagName: 'link',
      attributes: {rel: 'preload', href: '/fonts/Geist-SemiBold.ttf', as: 'font', type: 'font/ttf', crossorigin: 'anonymous'},
    },
  ],
  markdown: {
    mermaid: true,
    // future.v4 disables MDX v1 compat by default; keep admonition titles working
    // until all docs use :::tip[Title] directive-label syntax.
    mdx1Compat: {
      admonitions: true,
      comments: false,
    },
  },
  future: {
    v4: true,
  },

  // Public site URL (GitHub Pages on the public product repo)
  url: 'https://orion-belt.dev',
  baseUrl: '/',

  // Deploy target: gh-pages of the product repo this branch lives in
  organizationName: 'orion-belt-dev',
  projectName: 'orion-belt',
  deploymentBranch: 'gh-pages',
  trailingSlash: false,

  onBrokenLinks: 'throw',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          sidebarPath: './sidebars.js',
          routeBasePath: 'docs',
          lastVersion: '1.4.0',
          versions: {
            current: {
              label: 'Next',
              path: 'next',
              banner: 'unreleased',
            },
            '1.4.0': {
              label: '1.4.0',
              path: '',
              banner: 'none',
            },
            '1.3.0': {
              label: '1.3.0',
              path: '1.3.0',
              banner: 'none',
            },
            '1.2.0': {
              label: '1.2.0',
              path: '1.2.0',
              banner: 'none',
            },
            '1.1.0': {
              label: '1.1.0',
              path: '1.1.0',
              banner: 'none',
            },
            '1.0.0': {
              label: '1.0.0',
              path: '1.0.0',
              banner: 'none',
            },
          },
          editUrl: 'https://github.com/orion-belt-dev/orion-belt/edit/orion-belt-docs/',
        },
        blog: {
          showReadingTime: true,
          feedOptions: {
            type: ['rss', 'atom'],
            xslt: true,
          },
          editUrl: 'https://github.com/orion-belt-dev/orion-belt/edit/orion-belt-docs/',
          onInlineTags: 'warn',
          onInlineAuthors: 'warn',
          onUntruncatedBlogPosts: 'warn',
        },
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      image: 'img/orion-belt-social-card.png',
      metadata: [
        {
          name: 'description',
          content:
            'Orion Belt — self-hosted SSH access gateway with PAM workflows. Agents dial out, sessions recorded, JIT approvals, MFA.',
        },
      ],
      colorMode: {
        respectPrefersColorScheme: true,
      },
      navbar: {
        title: 'Orion Belt',
        logo: {
          alt: 'Orion Belt',
          src: 'img/orion-belt-mark-color-on-light-small.svg',
          srcDark: 'img/orion-belt-mark-color-on-dark-small.svg',
          width: 28,
          height: 28,
        },
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'tutorialSidebar',
            position: 'left',
            label: 'Documentation',
          },
          {
            to: '/docs/quickstart/get-started',
            label: 'Get started',
            position: 'left',
          },
          {
            to: '/docs/quickstart/try-in-10-minutes',
            label: 'Try in 10 min',
            position: 'left',
          },
          {to: '/blog', label: 'Blog', position: 'left'},
          {
            type: 'docsVersionDropdown',
            position: 'right',
            dropdownActiveClassDisabled: true,
          },
          {
            href: 'https://discord.gg/w62S8jxTHJ',
            label: 'Discord',
            position: 'right',
          },
          {
            href: 'https://github.com/orion-belt-dev/orion-belt',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              {label: 'Introduction', to: '/docs/introduction'},
              {label: 'Try in 10 minutes', to: '/docs/quickstart/try-in-10-minutes'},
              {label: 'Get started', to: '/docs/quickstart/get-started'},
              {label: 'Architecture', to: '/docs/architecture/overview'},
            ],
          },
          {
            title: 'Community',
            items: [
              {
                label: 'Discord',
                href: 'https://discord.gg/w62S8jxTHJ',
              },
              {
                label: 'GitHub Discussions',
                href: 'https://github.com/orion-belt-dev/orion-belt/discussions',
              },
              {
                label: 'Issues',
                href: 'https://github.com/orion-belt-dev/orion-belt/issues',
              },
            ],
          },
          {
            title: 'More',
            items: [
              {label: 'Blog', to: '/blog'},
              {
                label: 'GitHub',
                href: 'https://github.com/orion-belt-dev/orion-belt',
              },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Orion Belt Project. Built with Docusaurus.`,
      },
      prism: {
        theme: prismThemes.github,
        darkTheme: prismThemes.nightOwl,
        additionalLanguages: ['bash', 'yaml', 'go', 'json'],
      },
    }),
};

export default config;
