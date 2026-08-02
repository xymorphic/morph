import type * as Preset from '@docusaurus/preset-classic';
import type { Config } from '@docusaurus/types';
import { themes as prismThemes } from 'prism-react-renderer';

function getAlgoliaConfig(): Preset.ThemeConfig['algolia'] | undefined {
  const appId = process.env.DOCUSAURUS_ALGOLIA_APP_ID?.trim();
  const apiKey = process.env.DOCUSAURUS_ALGOLIA_API_KEY?.trim();
  const indexName = process.env.DOCUSAURUS_ALGOLIA_INDEX_NAME?.trim();
  const hasPartialConfig = Boolean(appId || apiKey || indexName);

  if (!hasPartialConfig) {
    return undefined;
  }

  const missing = [
    ['DOCUSAURUS_ALGOLIA_APP_ID', appId],
    ['DOCUSAURUS_ALGOLIA_API_KEY', apiKey],
    ['DOCUSAURUS_ALGOLIA_INDEX_NAME', indexName],
  ]
    .filter(([, value]) => !value)
    .map(([name]) => name);

  if (missing.length > 0) {
    throw new Error(`Missing Algolia search config: ${missing.join(', ')}`);
  }

  const configuredAppId = appId as string;
  const configuredApiKey = apiKey as string;
  const configuredIndexName = indexName as string;

  return {
    appId: configuredAppId,
    apiKey: configuredApiKey,
    indexName: configuredIndexName,
    contextualSearch: true,
    searchPagePath: 'search',
    insights: false,
  };
}

const algolia = getAlgoliaConfig();

const config: Config = {
  title: 'Morph',
  tagline: 'A terminal-first personal agent',
  favicon: 'img/favicon.ico',

  future: {
    v4: true,
  },

  url: 'https://morph.local',
  baseUrl: '/',

  headTags: [
    {
      tagName: 'link',
      attributes: {
        rel: 'preconnect',
        href: 'https://fonts.googleapis.com',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'preconnect',
        href: 'https://fonts.gstatic.com',
        crossorigin: 'anonymous',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'stylesheet',
        href: 'https://fonts.googleapis.com/css2?family=Geist+Mono:ital,wght@0,100..900;1,100..900&display=swap',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'stylesheet',
        href: 'https://fonts.googleapis.com/css2?family=Silkscreen:wght@400;700&display=swap',
      },
    },
  ],

  organizationName: 'xymorphic',
  projectName: 'morph',

  onBrokenLinks: 'throw',

  markdown: {
    mermaid: true,
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themes: ['@docusaurus/theme-mermaid'],

  themeConfig: {
    image: 'img/docusaurus-social-card.jpg',
    colorMode: {
      defaultMode: 'dark',
      respectPrefersColorScheme: true,
    },
    navbar: {
      logo: {
        alt: 'Morph',
        src: 'img/logo-black2.svg',
        srcDark: 'img/logo-white2.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docs',
          position: 'left',
          label: 'Guide',
          activeBaseRegex: '^/docs/(?!guides/gateway(?:/|$)|reference(?:/|$)).*',
        },
        {
          to: '/docs/guides/gateway',
          activeBaseRegex: '^/docs/guides/gateway(?:/|$)',
          label: 'Gateway',
          position: 'left',
        },
        {
          to: '/docs/reference/cli',
          activeBaseRegex: '^/docs/reference(?:/|$)',
          label: 'Reference',
          position: 'left',
        },
        ...(algolia
          ? [
              {
                type: 'search' as const,
                position: 'right' as const,
              },
            ]
          : []),
        {
          type: 'dropdown',
          label: 'Community',
          position: 'right',
          items: [
            {
              label: 'Forum',
              href: 'https://github.com/xymorphic/morph/discussions',
            },
            {
              label: 'Twitter',
              href: 'https://x.com/wandxy',
            },
            {
              label: 'Discord',
              href: 'https://discord.com/invite/wandxy',
            },
          ],
        },
        {
          to: '/docs/contributing',
          label: 'Contribute',
          position: 'right',
        },
        {
          type: 'custom-socialIcon',
          className: 'navbar-social-link-start',
          href: 'https://x.com/wandxy',
          icon: 'twitter',
          label: 'Twitter',
          position: 'right',
        },
        {
          type: 'custom-socialIcon',
          href: 'https://discord.com/invite/wandxy',
          icon: 'discord',
          label: 'Discord',
          position: 'right',
        },
        {
          type: 'custom-socialIcon',
          href: 'https://github.com/xymorphic/morph',
          icon: 'github',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'shell-session'],
    },
    ...(algolia ? {algolia} : {}),
  } satisfies Preset.ThemeConfig,
};

export default config;
