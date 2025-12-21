import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';

export const baseOptions: BaseLayoutProps = {
  nav: {
    title: (
      <div className="flex items-center gap-2">
        <span className="text-xl">🏄</span>
        <span className="font-bold">Surf</span>
      </div>
    ),
  },
  links: [
    {
      text: 'Documentation',
      url: '/docs',
      active: 'nested-url',
    },
    {
      text: 'GitHub',
      url: 'https://github.com/getangry/surf',
    },
  ],
  githubUrl: 'https://github.com/getangry/surf',
};
