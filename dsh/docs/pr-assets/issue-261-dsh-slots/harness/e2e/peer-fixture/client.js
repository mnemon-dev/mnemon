window.__ModuleLoader__.load({
  id: 'issue261-turn-tail-peers',
  factory: require => {
    const React = require('react');
    return {
      inject: ['slots'],
      apply(ctx) {
        for (const [id, label, order] of [
          ['issue261-peer-a:tail', 'Compatibility peer A', -20],
          ['issue261-peer-b:tail', 'Compatibility peer B', -10],
        ]) {
          ctx.slots.inject('conversation.chat.turnTail', () => ctx.slots.register({
            name: 'conversation.chat.turnTail', id, order, select: () => null,
          }, () => React.createElement('div', { 'data-compatibility-peer': id }, label)));
        }
      },
    };
  },
});
