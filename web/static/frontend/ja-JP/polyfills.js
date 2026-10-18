function s(t,e){for(let i=1,n=1;i<t.length;i++,n++)if(e[n]==="\\")n++;else if(t[i]===":")return i;throw new Error(`Unterminated $localize metadata block in "${e}".`)}var o=function(t,...e){if(o.translate){let n=o.translate(t,e);t=n[0],e=n[1]}let i=r(t[0],t.raw[0]);for(let n=1;n<t.length;n++)i+=e[n-1]+r(t[n],t.raw[n]);return i},l=":";function r(t,e){return e.charAt(0)===l?t.substring(s(t,e)+1):t}globalThis.$localize=o;(globalThis.$localize??={}).locale="ja-JP";
/**
 * @license
 * Copyright Google LLC All Rights Reserved.
 *
 * Use of this source code is governed by an MIT-style license that can be
 * found in the LICENSE file at https://angular.dev/license
 */

// THIS CODE IS GENERATED - DO NOT MODIFY.
  (function(global) {
    global.ng ??= {};
    global.ng.common ??= {};
    global.ng.common.locales ??= {};
    const u = undefined;
    function plural(val) {
const n = val;

return 5;
}
    global.ng.common.locales['ja'] = ["ja",[["午前","午後"]],u,[["日","月","火","水","木","金","土"],u,["日曜日","月曜日","火曜日","水曜日","木曜日","金曜日","土曜日"],["日","月","火","水","木","金","土"]],u,[["1","2","3","4","5","6","7","8","9","10","11","12"],["1月","2月","3月","4月","5月","6月","7月","8月","9月","10月","11月","12月"]],u,[["BC","AD"],["紀元前","西暦"]],0,[6,0],["y/MM/dd",u,"y年M月d日","y年M月d日EEEE"],["H:mm","H:mm:ss","H:mm:ss z","H時mm分ss秒 zzzz"],["{1} {0}",u,u,u],[".",",",";","%","+","-","E","×","‰","∞","NaN",":"],["#,##0.###","#,##0%","¤#,##0.00","#E0"],"JPY","￥","日本円",{"BYN":[u,"р."],"CNY":["元","￥"],"JPY":["￥"],"PHP":[u,"₱"],"RON":[u,"レイ"],"XXX":[]},"ltr", plural, [[["真夜中","正午","朝","昼","夕方","夜","夜中"],u,u],u,["00:00","12:00",["04:00","12:00"],["12:00","16:00"],["16:00","19:00"],["19:00","23:00"],["23:00","04:00"]]]];
  })(globalThis);
    
;;
/**i18n:fa3d7e19b336ec75*/
