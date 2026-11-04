package sitecfg

var templateJSON = []byte(`{
  "sections": [
    {
      "name": "HQHOST",
      "entries": [
        {
          "key": "HostNum",
          "value": "12"
        },
        {
          "key": "PrimaryHost",
          "value": "2"
        },
        {
          "key": "HostNameNN",
          "value": "银河证券腾讯云行情"
        },
        {
          "key": "IPAddressNN",
          "value": "114.117.72.207"
        },
        {
          "key": "PortNN",
          "value": "7709"
        },
        {
          "key": "WeightNN",
          "value": "90"
        },
        {
          "key": "AreasNN",
          "value": ""
        }
      ]
    },
    {
      "name": "TRYHQHOST",
      "entries": [
        {
          "key": "HostNum",
          "value": "9"
        },
        {
          "key": "PrimaryHost",
          "value": "0"
        },
        {
          "key": "HostNameNN",
          "value": "银河证券腾讯云行情"
        },
        {
          "key": "IPAddressNN",
          "value": "114.117.72.207"
        },
        {
          "key": "PortNN",
          "value": "7709"
        },
        {
          "key": "WeightNN",
          "value": "90"
        },
        {
          "key": "AreasNN",
          "value": ""
        }
      ]
    },
    {
      "name": "INFOHOST",
      "entries": [
        {
          "key": "AUTOPOP",
          "value": "1"
        },
        {
          "key": "InfoURLStr",
          "value": ""
        },
        {
          "key": "InfoURL",
          "value": ""
        },
        {
          "key": "EXTERNINFO",
          "value": "0"
        },
        {
          "key": "INFO",
          "value": "1"
        },
        {
          "key": "HostNum",
          "value": "10"
        },
        {
          "key": "PrimaryHost",
          "value": "7"
        },
        {
          "key": "HostNameNN",
          "value": "银河证券腾讯云资讯"
        },
        {
          "key": "IPAddressNN",
          "value": "114.117.72.207"
        },
        {
          "key": "PortNN",
          "value": "7711"
        },
        {
          "key": "AreasNN",
          "value": ""
        },
        {
          "key": "HostTypeNN",
          "value": "0"
        }
      ]
    },
    {
      "name": "WTHOST",
      "entries": [
        {
          "key": "WT",
          "value": "1"
        },
        {
          "key": "EXTERNWT",
          "value": "1"
        },
        {
          "key": "WTFACE",
          "value": "1"
        },
        {
          "key": "HostNum",
          "value": "10"
        },
        {
          "key": "PrimaryHost",
          "value": "0"
        },
        {
          "key": "HostNameNN",
          "value": "银河证券北京网通"
        },
        {
          "key": "IPAddressNN",
          "value": "61.135.173.138"
        },
        {
          "key": "PortNN",
          "value": "7708"
        },
        {
          "key": "HexinDLLWt",
          "value": "0"
        },
        {
          "key": "AUTOIN",
          "value": "0"
        }
      ]
    },
    {
      "name": "Other",
      "entries": [
        {
          "key": "showMrPin",
          "value": "2"
        },
        {
          "key": "OtherBtURL",
          "value": "http://www.chinastock.com.cn/yhwz/service/BespeakAccount.shtml"
        },
        {
          "key": "LCF10Url",
          "value": "http://www.chinastock.com.cn/yhwz/managemoney/manageMoneyAcion.do?methodCall=productForClient&code=xxxxxx&flag=gaikuang"
        },
        {
          "key": "CacheFileName",
          "value": "flash.zip"
        },
        {
          "key": "SupportStatusRoll",
          "value": "1"
        },
        {
          "key": "DSAutoStopMinute",
          "value": "10"
        },
        {
          "key": "SaveEmbedTabSel",
          "value": "0"
        },
        {
          "key": "HasHomePage",
          "value": "1"
        },
        {
          "key": "HasJSHomePage",
          "value": "1"
        },
        {
          "key": "JSHomePageUseT2EE",
          "value": "0"
        },
        {
          "key": "HasTopHomePage",
          "value": "1"
        },
        {
          "key": "WTFNN",
          "value": "1"
        },
        {
          "key": "HideLogoTab",
          "value": "1"
        },
        {
          "key": "TurnLogo",
          "value": "0"
        },
        {
          "key": "RiskPost",
          "value": "1"
        },
        {
          "key": "DgzqStyle",
          "value": "0"
        },
        {
          "key": "ForbidSameTime",
          "value": "0"
        },
        {
          "key": "AutoConnectIn",
          "value": "1"
        },
        {
          "key": "AlwaysGuest",
          "value": "0"
        },
        {
          "key": "InitTcpipMode",
          "value": "0"
        },
        {
          "key": "ShowVersoin",
          "value": "1"
        },
        {
          "key": "NoDiskMode",
          "value": "0"
        },
        {
          "key": "PreName",
          "value": "海王星金融终端v3.0-中国银河证券"
        },
        {
          "key": "TitleName",
          "value": "海王星金融终端v3.0-中国银河证券"
        },
        {
          "key": "Corp",
          "value": "银河证券"
        },
        {
          "key": "CorpWeb",
          "value": "www.chinastock.com.cn"
        },
        {
          "key": "AutoUpZip",
          "value": "tdxw3_yhzqnewjy_tcpip"
        },
        {
          "key": "ForumURL",
          "value": "cdns.chinastock.com.cn/cdn/online-client/#/chat"
        },
        {
          "key": "WebInfoINI",
          "value": "webinfo.ini"
        },
        {
          "key": "HELPURL",
          "value": ""
        },
        {
          "key": "SysMark",
          "value": "银河证券"
        },
        {
          "key": "LogoAdvert",
          "value": "欢迎使用中国银河证券海王星金融终端v11.68[支持IPV6]"
        },
        {
          "key": "HasWebInfo",
          "value": "0"
        },
        {
          "key": "WTPreNAME",
          "value": "海王星金融终端v3.0-中国银河证券"
        },
        {
          "key": "HIDESAVEZH",
          "value": "0"
        },
        {
          "key": "MinAuthPassLen",
          "value": "1"
        },
        {
          "key": "WtUserVerifyStyle",
          "value": "0"
        },
        {
          "key": "EnableNLB",
          "value": "0"
        },
        {
          "key": "HintSwitch",
          "value": "0"
        },
        {
          "key": "AutoupURLDir",
          "value": "www.tdx.com.cn/products/autoup/"
        },
        {
          "key": "ALLEQUAL",
          "value": "0"
        },
        {
          "key": "SHOWGBINFO",
          "value": "0"
        },
        {
          "key": "TREEQUIT",
          "value": "0"
        },
        {
          "key": "HasSSL",
          "value": "0"
        },
        {
          "key": "RandSwitch",
          "value": "1"
        },
        {
          "key": "UseLocalData",
          "value": "0"
        },
        {
          "key": "DiffVolMode",
          "value": "0"
        },
        {
          "key": "AtOnceDown",
          "value": "0"
        },
        {
          "key": "ForbidOtherHost",
          "value": "0"
        },
        {
          "key": "CanUsedConnectEver",
          "value": "0"
        },
        {
          "key": "RestRightBtn",
          "value": "0"
        },
        {
          "key": "EveryFirstHost",
          "value": "0"
        },
        {
          "key": "HasUserHost",
          "value": "0"
        },
        {
          "key": "HasDS",
          "value": "1"
        },
        {
          "key": "WTF9",
          "value": "1"
        },
        {
          "key": "TreeCloseBtn",
          "value": "1"
        },
        {
          "key": "HasJBFX",
          "value": "0"
        },
        {
          "key": "HasChat",
          "value": "0"
        },
        {
          "key": "HasTICQ",
          "value": "0"
        },
        {
          "key": "AutoConnectTICQ",
          "value": "0"
        },
        {
          "key": "HasCustomPad",
          "value": "1"
        },
        {
          "key": "HasShx",
          "value": "0"
        },
        {
          "key": "docpath",
          "value": ""
        },
        {
          "key": "WebKeyGuy",
          "value": "2"
        },
        {
          "key": "Show3MMP",
          "value": "0"
        },
        {
          "key": "AutoLogo",
          "value": "0"
        },
        {
          "key": "ReConnectNum",
          "value": "0"
        },
        {
          "key": "EmailStr",
          "value": "客服电话 95551"
        },
        {
          "key": "EmailURL",
          "value": "www.chinastock.com.cn/yhwz/service/index.shtml"
        },
        {
          "key": "NeedPlugExe",
          "value": "0"
        },
        {
          "key": "F10Cache1Day",
          "value": "1"
        },
        {
          "key": "AddBlockPrompt",
          "value": "0"
        },
        {
          "key": "SimpleZxMenu",
          "value": "0"
        },
        {
          "key": "LogoTimeOut",
          "value": "10"
        },
        {
          "key": "GLSCVersion",
          "value": "0"
        },
        {
          "key": "HasExDay",
          "value": "0"
        },
        {
          "key": "HasZHB",
          "value": "0"
        },
        {
          "key": "CompatibleSzCode",
          "value": "0"
        },
        {
          "key": "FXJSSADDRESS",
          "value": ""
        },
        {
          "key": "SHOWFASTCHECK",
          "value": "0"
        },
        {
          "key": "GETFASTHOST",
          "value": "0"
        },
        {
          "key": "LOGINKOULING",
          "value": "0"
        },
        {
          "key": "AUTOUPDATE",
          "value": "1"
        },
        {
          "key": "CYFIRST",
          "value": "0"
        },
        {
          "key": "CLEAN",
          "value": "1"
        },
        {
          "key": "GPFILTER",
          "value": "0"
        },
        {
          "key": "AboutURL",
          "value": ""
        },
        {
          "key": "HasWebMovingInfo",
          "value": "0"
        },
        {
          "key": "AlwaysMovingInfo",
          "value": "0"
        },
        {
          "key": "WebDownInterval",
          "value": "30"
        },
        {
          "key": "WebMovingUrl",
          "value": ""
        },
        {
          "key": "NoAutoup",
          "value": "0"
        },
        {
          "key": "NBBS",
          "value": "tdx"
        },
        {
          "key": "ShareMode",
          "value": "0"
        },
        {
          "key": "HabitPath",
          "value": ""
        },
        {
          "key": "ShortRefreshHQ",
          "value": "0"
        },
        {
          "key": "OtherFileName",
          "value": "customcfg_yhzqjrzd1131.zip"
        },
        {
          "key": "WhichAutoUpInfo",
          "value": "15"
        },
        {
          "key": "LittleFluxVersion",
          "value": "0"
        },
        {
          "key": "MaxFluxCount",
          "value": "19200000"
        },
        {
          "key": "NLBOnLogo",
          "value": "0"
        },
        {
          "key": "Version_Title",
          "value": ""
        },
        {
          "key": "MainWebTitle",
          "value": "券商网"
        },
        {
          "key": "OnlyOne",
          "value": "0"
        },
        {
          "key": "Warn",
          "value": "1"
        },
        {
          "key": "WebInfoURL",
          "value": ""
        },
        {
          "key": "HelpWebStr",
          "value": "应急措施公告"
        },
        {
          "key": "HelpWebURL",
          "value": "https://www.chinastock.com.cn/newsite/online/announcementDetail.html?docId=6956142"
        },
        {
          "key": "HelpWebStr2",
          "value": "隐私政策"
        },
        {
          "key": "HelpWebURL2",
          "value": "https://cdns.chinastock.com.cn/cdn/privacy-third/otc/index.html"
        },
        {
          "key": "AutoupFromHQHost",
          "value": "0"
        },
        {
          "key": "TRADEAUTOUP",
          "value": "0"
        },
        {
          "key": "APPLYONLINE",
          "value": "0"
        },
        {
          "key": "NLBLESS",
          "value": "0"
        },
        {
          "key": "ShowZjzhInCombo",
          "value": "1"
        },
        {
          "key": "ShowProtect",
          "value": "1"
        },
        {
          "key": "ALLONESAVE",
          "value": "0"
        },
        {
          "key": "WTColorIcon",
          "value": "0"
        },
        {
          "key": "ShowWTServerName",
          "value": "0"
        },
        {
          "key": "EmbedTabSel",
          "value": "0"
        },
        {
          "key": "TdxInfoIniName",
          "value": "tdxinfo.ini"
        },
        {
          "key": "AddZxURL",
          "value": ""
        },
        {
          "key": "AddZxStr",
          "value": ""
        },
        {
          "key": "LogoHelpURL",
          "value": ""
        },
        {
          "key": "LogoHelpStr",
          "value": ""
        },
        {
          "key": "AutoupMakeLinkName",
          "value": ""
        },
        {
          "key": "NoUrgent",
          "value": "0"
        },
        {
          "key": "NetErrorReLogin",
          "value": "1"
        },
        {
          "key": "HasBigToolBar",
          "value": "0"
        },
        {
          "key": "UserGuideBtURL",
          "value": "http://www.treeid/MACHINEBIND"
        },
        {
          "key": "WebPageNum",
          "value": "7"
        },
        {
          "key": "WebPageStrNN",
          "value": "银河证券网"
        },
        {
          "key": "WebPageURLNN",
          "value": "http://www.treeid/inhttp://www.chinastock.com.cn"
        }
      ]
    },
    {
      "name": "EXTERN",
      "entries": [
        {
          "key": "SessionUrl",
          "value": "##homepath##jspages\\zxg\\dist\\index.html#/AutoSync"
        },
        {
          "key": "WTPATH1",
          "value": ""
        },
        {
          "key": "WTNAME1",
          "value": ""
        },
        {
          "key": "WTPATH0",
          "value": ""
        },
        {
          "key": "WTNAME0",
          "value": ""
        },
        {
          "key": "AlongPathFile",
          "value": ""
        },
        {
          "key": "WebPageName0",
          "value": "图解财报"
        },
        {
          "key": "WebPageURL0",
          "value": "http://www.chinastock.com.cn/thirdclient/SingleNews.jsp?symbol=xxxxxx"
        },
        {
          "key": "WebPageName1",
          "value": "在线客服"
        },
        {
          "key": "WebPageURL1",
          "value": "http://www.chinastock.com.cn/thirdclient/ClientSupport.jsp?actionType=checkRevert&qvalidata=tdx_yhzqcode"
        },
        {
          "key": "WebPageName2",
          "value": "银河研究"
        },
        {
          "key": "WebPageURL2",
          "value": "http://www.chinastock.com.cn/thirdclient/baodian.jsp?actionType=checkRevert&qvalidata=tdx_yhzqcode"
        },
        {
          "key": "WebPageName3",
          "value": "公告信息"
        },
        {
          "key": "WebPageURL3",
          "value": "http://www.chinastock.com.cn/thirdclient/hsts.shtml"
        },
        {
          "key": "F11WebPageURL",
          "value": ""
        },
        {
          "key": "F11WebPageName",
          "value": ""
        },
        {
          "key": "HKey_Path",
          "value": ""
        },
        {
          "key": "HKey_KeyString",
          "value": ""
        },
        {
          "key": "AlongJy",
          "value": "0"
        },
        {
          "key": "AlongOpenWT",
          "value": "0"
        },
        {
          "key": "AlongOpenHXInfo",
          "value": "0"
        }
      ]
    },
    {
      "name": "user",
      "entries": [
        {
          "key": "savepass",
          "value": "0"
        },
        {
          "key": "UserName",
          "value": ""
        }
      ]
    },
    {
      "name": "PROXY",
      "entries": [
        {
          "key": "HasProxy",
          "value": "0"
        },
        {
          "key": "SockType",
          "value": "2"
        },
        {
          "key": "ProxyPort",
          "value": "8080"
        },
        {
          "key": "ProxyIP",
          "value": "192.168.0.188"
        },
        {
          "key": "ProxyUser",
          "value": ""
        },
        {
          "key": "ProxyPass",
          "value": ""
        }
      ]
    },
    {
      "name": "PHONE",
      "entries": [
        {
          "key": "PHONENUM",
          "value": "0"
        },
        {
          "key": "PrimaryPhone",
          "value": "-1"
        }
      ]
    },
    {
      "name": "INFOUSER",
      "entries": [
        {
          "key": "UserName",
          "value": "guest"
        },
        {
          "key": "SavePass",
          "value": "0"
        }
      ]
    },
    {
      "name": "Integrity",
      "entries": [
        {
          "key": "ChatAreaNum",
          "value": "3"
        },
        {
          "key": "ChatAreaStrNN",
          "value": "信息地雷"
        },
        {
          "key": "AreaTypeNN",
          "value": "4"
        }
      ]
    },
    {
      "name": "VitalBtn",
      "entries": [
        {
          "key": "BtnNum",
          "value": "3"
        },
        {
          "key": "BtnStrNN",
          "value": "行情系统"
        },
        {
          "key": "BtnTypeNN",
          "value": "4"
        }
      ]
    },
    {
      "name": "IETOOLBAR",
      "entries": [
        {
          "key": "TOOLNAME",
          "value": "TdxW.exe"
        },
        {
          "key": "TOOLFLAG",
          "value": "0"
        },
        {
          "key": "TOOLKEY",
          "value": ""
        },
        {
          "key": "TOOLTEXT",
          "value": "海王星金融终端v3.0-中国银河证券"
        },
        {
          "key": "TOOLPATH",
          "value": "海王星金融终端v3.0-中国银河证券"
        }
      ]
    },
    {
      "name": "UserHost",
      "entries": [
        {
          "key": "WtUserVerifyStyle",
          "value": "1"
        },
        {
          "key": "MiscJyStyle",
          "value": "0"
        },
        {
          "key": "HasAutoRelation",
          "value": "0"
        },
        {
          "key": "HasWebRelation",
          "value": "0"
        },
        {
          "key": "GuestLogin",
          "value": "0"
        },
        {
          "key": "RegisterLogin",
          "value": "1"
        },
        {
          "key": "JyLogin",
          "value": "0"
        },
        {
          "key": "DialogRegister",
          "value": "0"
        },
        {
          "key": "Port",
          "value": "7715"
        },
        {
          "key": "BakPort",
          "value": "7715"
        },
        {
          "key": "GuestLogin_Str",
          "value": "浏览用户"
        },
        {
          "key": "RegisterLogin_Str",
          "value": "资金帐号方式"
        },
        {
          "key": "JyLogin_Str",
          "value": "CA证书方式"
        },
        {
          "key": "IPAddress",
          "value": ""
        },
        {
          "key": "BakIPAddress",
          "value": ""
        },
        {
          "key": "CanDelMySelf",
          "value": "0"
        },
        {
          "key": "OnlyWebUser",
          "value": "0"
        },
        {
          "key": "Web_RegisterURL",
          "value": ""
        },
        {
          "key": "Web_UserInfoURL",
          "value": ""
        },
        {
          "key": "EnableUserName",
          "value": "1"
        },
        {
          "key": "UserHostEverConnect",
          "value": "0"
        },
        {
          "key": "JyTimeOption",
          "value": "0"
        },
        {
          "key": "ReVerifySpan",
          "value": "20"
        },
        {
          "key": "SpecIPLogin",
          "value": "0"
        },
        {
          "key": "SpecIPLogin_Str",
          "value": "总部用户"
        },
        {
          "key": "QSID",
          "value": "银河证券"
        },
        {
          "key": "CloudQSID",
          "value": "银河证券"
        },
        {
          "key": "HexinWtOnlyTXMM",
          "value": "0"
        },
        {
          "key": "CheckUserURL",
          "value": ""
        }
      ]
    },
    {
      "name": "CHATHOST",
      "entries": [
        {
          "key": "Port",
          "value": "7710"
        },
        {
          "key": "IPAddress",
          "value": ""
        }
      ]
    },
    {
      "name": "ICQHOST",
      "entries": [
        {
          "key": "Port",
          "value": "7716"
        },
        {
          "key": "IPAddress",
          "value": ""
        }
      ]
    },
    {
      "name": "SHELLEXE",
      "entries": [
        {
          "key": "EXENUM",
          "value": "1"
        },
        {
          "key": "EXENAMENN",
          "value": "TdxSP.exe"
        },
        {
          "key": "EXEDESCNN",
          "value": "高清显示"
        }
      ]
    },
    {
      "name": "TDXPROXY",
      "entries": [
        {
          "key": "HasTdxProxy",
          "value": "1"
        },
        {
          "key": "DESC",
          "value": "银河证券杭州电信"
        },
        {
          "key": "IP",
          "value": "218.75.75.18"
        },
        {
          "key": "Port",
          "value": "8443"
        },
        {
          "key": "DESC2",
          "value": "银河证券杭州联通"
        },
        {
          "key": "IP2",
          "value": "101.71.255.135"
        },
        {
          "key": "Port2",
          "value": "8443"
        }
      ]
    },
    {
      "name": "STARTMENU",
      "entries": [
        {
          "key": "HASHDIR",
          "value": "0"
        },
        {
          "key": "HDIRNAME",
          "value": ""
        }
      ]
    },
    {
      "name": "Version",
      "entries": [
        {
          "key": "JYElderStyle",
          "value": "1"
        },
        {
          "key": "FirstSetupBSTokenClose",
          "value": "1"
        },
        {
          "key": "HasSSLSel",
          "value": "1"
        },
        {
          "key": "TdxCloudZxUrl",
          "value": "http://##tdxcloudaddress_tp2##/site/tdx-pc-ggzx/page-zx.html?from=yhzq&code=xxxxxx&setcode=##SETCODE##&typeid=##zxstr##&more=1&color=##tdxbk##&bkcolor=##tdxbkcolor##"
        },
        {
          "key": "TdxCloudBkUrl",
          "value": "http://##tdxcloudaddress_tp2##/site/tdx-pc-ggzx/page-zx.html?from=yhzq&code=xxxxxx&setcode=##SETCODE##&typeid=bkxw&color=##tdxbk##&bkcolor=##tdxbkcolor##"
        },
        {
          "key": "TdxCloudAddress_TP",
          "value": "123.125.135.12:7199;103.251.85.235:7199"
        },
        {
          "key": "TdxCloudAddress_TP2",
          "value": "page1.icfqs.com:7615;page2.icfqs.com:7615;page3.icfqs.com:7615"
        },
        {
          "key": "ClassXPType",
          "value": "1"
        },
        {
          "key": "NewJy",
          "value": "7"
        },
        {
          "key": "HasVipJy",
          "value": "0"
        },
        {
          "key": "HasCXMode",
          "value": "0"
        },
        {
          "key": "InsideVersion",
          "value": "0"
        },
        {
          "key": "OnlyHK",
          "value": "0"
        },
        {
          "key": "HasLeftHQ",
          "value": "1"
        },
        {
          "key": "ViewthemUseChrome",
          "value": "2"
        },
        {
          "key": "HasSBLevin",
          "value": "1"
        },
        {
          "key": "AutoChangeSSLByAqfs",
          "value": "1"
        },
        {
          "key": "FirstSetupGMSSL",
          "value": "1"
        },
        {
          "key": "PageNoSupportXP",
          "value": "1"
        },
        {
          "key": "hastl",
          "value": "2"
        }
      ]
    },
    {
      "name": "DSHOST",
      "entries": [
        {
          "key": "HostNum",
          "value": "3"
        },
        {
          "key": "PrimaryHost",
          "value": "2"
        },
        {
          "key": "HostNameNN",
          "value": "银河腾讯云扩展行情"
        },
        {
          "key": "IPAddressNN",
          "value": "114.117.72.207"
        },
        {
          "key": "PortNN",
          "value": "7720"
        },
        {
          "key": "AreasNN",
          "value": ""
        }
      ]
    },
    {
      "name": "OTHERHOST",
      "entries": [
        {
          "key": "L2PrimaryHost",
          "value": "0"
        },
        {
          "key": "SSDSPrimaryHost",
          "value": "0"
        },
        {
          "key": "SSDS2PrimaryHost",
          "value": "0"
        },
        {
          "key": "SSDS3PrimaryHost",
          "value": "0"
        },
        {
          "key": "VipHQPrimaryHost",
          "value": "-1"
        },
        {
          "key": "VipHQ2PrimaryHost",
          "value": "-1"
        }
      ]
    },
    {
      "name": "TPSYS",
      "entries": [
        {
          "key": "TPInitInFrame",
          "value": "1"
        },
        {
          "key": "DelayConnect",
          "value": "1,300"
        },
        {
          "key": "HasLog",
          "value": "0"
        },
        {
          "key": "Log",
          "value": "0"
        },
        {
          "key": "SMT",
          "value": "0"
        },
        {
          "key": "CheckUpgrade",
          "value": "1"
        },
        {
          "key": "UseMT",
          "value": "1"
        },
        {
          "key": "TimeLimit",
          "value": "3000"
        },
        {
          "key": "TPCanAnonymous",
          "value": "1"
        },
        {
          "key": "TimeOut",
          "value": "10"
        },
        {
          "key": "PrimaryTPHost",
          "value": "0"
        },
        {
          "key": "TPHostNum",
          "value": "8"
        },
        {
          "key": "TPHostNameNN",
          "value": "银河证券北京电信(HSY)"
        },
        {
          "key": "TPHostNN",
          "value": "219.143.214.206"
        },
        {
          "key": "TPPortNN",
          "value": "9914,0"
        },
        {
          "key": "AreasNN",
          "value": ""
        },
        {
          "key": "TPUseIPV6NN",
          "value": "0"
        },
        {
          "key": "NeedRegPhone",
          "value": "1"
        }
      ]
    },
    {
      "name": "TPSUBSYS",
      "entries": [
        {
          "key": "SMT",
          "value": "20"
        },
        {
          "key": "TPSubPrefix",
          "value": "ZXG,YHLogin"
        },
        {
          "key": "TPHostNum",
          "value": "9"
        },
        {
          "key": "PrimaryTPHost",
          "value": "0"
        },
        {
          "key": "TPHostNN",
          "value": "218.205.130.77"
        },
        {
          "key": "TPPortNN",
          "value": "9915,0"
        },
        {
          "key": "TPHostNameNN",
          "value": ""
        },
        {
          "key": "AreasNN",
          "value": ""
        },
        {
          "key": "TPUseIPV6NN",
          "value": "0"
        }
      ]
    },
    {
      "name": "Autoup",
      "entries": [
        {
          "key": "AutoupNewVer",
          "value": "1"
        }
      ]
    }
  ]
}
`)
