module.exports = function(api) {
  const base = require('@rancher/shell/babel.config.js')(api);

  base.presets = base.presets || [];
  base.presets.push('@babel/preset-typescript');

  if (base.env && base.env.test) {
    base.env.test.presets = base.env.test.presets || [];
    base.env.test.presets.push('@babel/preset-typescript');
  }

  return base;
};
